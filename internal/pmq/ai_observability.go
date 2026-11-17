package pmq

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

type aiMetrics struct {
	requests, usageMissing, limitRejected *prometheus.CounterVec
	active                                *prometheus.GaugeVec
	duration, ttft                        *prometheus.HistogramVec
	tokens                                *prometheus.CounterVec
}

func newAIMetrics(r *prometheus.Registry) *aiMetrics {
	m := &aiMetrics{
		requests:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "pmq_ai_requests_total", Help: "AI gateway requests by bounded outcome."}, []string{"provider", "model_alias", "outcome", "error"}),
		active:        prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "pmq_ai_requests_in_flight", Help: "AI gateway requests currently in flight."}, []string{"provider", "model_alias"}),
		duration:      prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "pmq_ai_request_duration_seconds", Help: "AI provider request duration in seconds.", Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60}}, []string{"provider", "model_alias", "outcome"}),
		ttft:          prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "pmq_ai_time_to_first_token_seconds", Help: "AI stream time to first token in seconds.", Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}}, []string{"provider", "model_alias"}),
		tokens:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "pmq_ai_tokens_total", Help: "AI tokens reported by provider usage metadata."}, []string{"provider", "model_alias", "direction"}),
		usageMissing:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "pmq_ai_usage_missing_total", Help: "AI successful requests without complete token usage."}, []string{"provider", "model_alias"}),
		limitRejected: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "pmq_ai_limit_rejections_total", Help: "AI requests rejected by configured limits."}, []string{"reason"}),
	}
	r.MustRegister(m.requests, m.active, m.duration, m.ttft, m.tokens, m.usageMissing, m.limitRejected)
	return m
}

func boundedAIError(err error, ctx context.Context) string {
	if err == nil {
		return "none"
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var ae *aiError
	if errors.As(err, &ae) {
		switch ae.code {
		case "invalid_request", "unsupported_model", "provider_mismatch", "streaming_unsupported":
			return "request"
		case "ai_rate_limited", "ai_concurrency_limited", "ai_budget_exceeded":
			return "limit"
		}
	}
	return "provider"
}

func configuredAIIdentity(cfg AIConfig, body []byte) (string, string) {
	var req ChatRequest
	if jsonUnmarshalAI(body, &req) != nil {
		return "unknown", "unknown"
	}
	alias := req.Model
	if alias == "" {
		alias = cfg.DefaultModel
	}
	if b, ok := cfg.Models[alias]; ok {
		return boundedProvider(b.Provider), alias
	}
	return "unknown", "unknown"
}

// Kept as a tiny helper so observability does not retain or log request bodies.
func jsonUnmarshalAI(body []byte, req *ChatRequest) error { return json.Unmarshal(body, req) }

func boundedProvider(p string) string {
	switch strings.ToLower(p) {
	case "openai", "openrouter", "bedrock":
		return strings.ToLower(p)
	}
	return "unknown"
}

func validAIModelAlias(alias string) bool {
	if len(alias) == 0 || len(alias) > 64 {
		return false
	}
	for _, r := range alias {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r)) {
			return false
		}
	}
	return true
}

func (s *Server) recordAIResult(provider, alias string, started time.Time, err error, ctx context.Context) {
	outcome, category := "success", "none"
	if err != nil {
		outcome, category = "failure", boundedAIError(err, ctx)
	}
	s.aiMetrics.requests.WithLabelValues(provider, alias, outcome, category).Inc()
	s.aiMetrics.duration.WithLabelValues(provider, alias, outcome).Observe(time.Since(started).Seconds())
}

func (s *Server) recordAIUsage(provider, alias string, usage *ChatUsage) {
	if !validChatUsage(usage) {
		s.aiMetrics.usageMissing.WithLabelValues(provider, alias).Inc()
		return
	}
	s.aiMetrics.tokens.WithLabelValues(provider, alias, "input").Add(float64(usage.PromptTokens))
	s.aiMetrics.tokens.WithLabelValues(provider, alias, "output").Add(float64(usage.CompletionTokens))
}

func newAITracer(endpoint string) (*sdktrace.TracerProvider, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !(parsed.Scheme == "https" || parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1")) {
		return nil, errors.New("OTLP trace endpoint must use HTTPS (loopback HTTP is allowed)")
	}
	exporter, err := otlptracehttp.New(context.Background(), otlptracehttp.WithEndpointURL(endpoint))
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("pmq"))))
	return tp, nil
}

func safeAITraceParent(ctx context.Context, header string) context.Context {
	if header == "" {
		return ctx
	}
	ctx = propagation.TraceContext{}.Extract(ctx, propagation.HeaderCarrier(http.Header{"Traceparent": []string{header}}))
	remote := trace.SpanContextFromContext(ctx)
	if !remote.IsValid() {
		return ctx
	}
	// Discard remotely supplied tracestate. Only the W3C trace/span IDs and sampling bit propagate.
	clean := trace.NewSpanContext(trace.SpanContextConfig{TraceID: remote.TraceID(), SpanID: remote.SpanID(), TraceFlags: remote.TraceFlags(), Remote: true})
	return trace.ContextWithRemoteSpanContext(ctx, clean)
}

type aiTraceRoundTripper struct{ base http.RoundTripper }

func (t aiTraceRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if !trace.SpanContextFromContext(r.Context()).IsValid() {
		return t.base.RoundTrip(r)
	}
	clone := r.Clone(r.Context())
	clone.Header = r.Header.Clone()
	// Propagate only the W3C trace context. Baggage and all request data are omitted.
	clone.Header.Del("baggage")
	clone.Header.Del("tracestate")
	propagation.TraceContext{}.Inject(r.Context(), propagation.HeaderCarrier(clone.Header))
	return t.base.RoundTrip(clone)
}
