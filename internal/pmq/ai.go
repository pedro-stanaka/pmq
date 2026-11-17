package pmq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/bedrock"
	"github.com/openai/openai-go/v3/option"
)

const maxAIRequest = 1 << 20
const maxAIResponse = 2 << 20

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Stop        []string      `json:"stop,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

type ChatChoice struct {
	Index   int `json:"index"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	FinishReason string `json:"finish_reason"`
}

type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   *ChatUsage   `json:"usage,omitempty"`
}

// ChatProvider is the narrow upstream contract used by the AI HTTP handler.
type ChatProvider interface {
	Complete(context.Context, json.RawMessage) (json.RawMessage, error)
}

type AIConfig struct {
	Provider      string
	APIKey        string
	BaseURL       string
	AWSRegion     string
	Models        map[string]string // public model name -> provider model ID
	BedrockModels map[string]string // public model name -> configured Bedrock model ID
	HTTPClient    *http.Client
}

type aiError struct {
	status int
	code   string
	msg    string
}

func (e *aiError) Error() string { return e.msg }

var errAIResponseTooLarge = errors.New("AI provider response exceeds 2 MiB")

type responseLimitTransport struct{ base http.RoundTripper }

func (t responseLimitTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err == nil && resp.Body != nil {
		resp.Body = &responseLimitBody{ReadCloser: resp.Body, remaining: maxAIResponse}
	}
	return resp, err
}

type responseLimitBody struct {
	io.ReadCloser
	remaining int64
}

func (b *responseLimitBody) Read(p []byte) (int, error) {
	if int64(len(p)) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	if int64(n) > b.remaining {
		return int(b.remaining), errAIResponseTooLarge
	}
	b.remaining -= int64(n)
	return n, err
}

func boundedAIHTTPClient(c *http.Client) *http.Client {
	client := &http.Client{}
	if c != nil {
		*client = *c
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = responseLimitTransport{base: base}
	return client
}

func validAIBaseURL(base string) bool {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	loopbackHTTP := u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")
	return u.Scheme == "https" || loopbackHTTP
}

func newAIProvider(c AIConfig) (ChatProvider, error) {
	provider := strings.ToLower(strings.TrimSpace(c.Provider))
	if provider == "" || provider == "off" {
		return nil, nil
	}
	clientOpts := []option.RequestOption{option.WithHTTPClient(boundedAIHTTPClient(c.HTTPClient))}
	var client openai.Client
	models := c.Models
	switch provider {
	case "openai", "openrouter":
		if c.APIKey == "" {
			return nil, fmt.Errorf("%s API key is required", provider)
		}
		if len(models) == 0 {
			return nil, errors.New("PMQ_AI_MODELS must configure allowed model aliases")
		}
		base := c.BaseURL
		if base == "" {
			if provider == "openrouter" {
				base = "https://openrouter.ai/api/v1"
			} else {
				base = "https://api.openai.com/v1"
			}
		}
		if !validAIBaseURL(base) {
			return nil, errors.New("AI base URL must use HTTPS without userinfo, query, or fragment (loopback HTTP is allowed for tests)")
		}
		clientOpts = append(clientOpts, option.WithBaseURL(base), option.WithAPIKey(c.APIKey))
		client = openai.NewClient(clientOpts...)
	case "bedrock":
		if len(c.BedrockModels) == 0 {
			return nil, errors.New("PMQ_BEDROCK_MODELS must configure supported Chat Completions model IDs")
		}
		models = c.BedrockModels
		cfg := bedrock.Config{AWSRegion: c.AWSRegion, Endpoint: bedrock.EndpointRuntime}
		if c.BaseURL != "" {
			if !validAIBaseURL(c.BaseURL) {
				return nil, errors.New("Bedrock base URL must use HTTPS without userinfo, query, or fragment (loopback HTTP is allowed for tests)")
			}
			cfg.BaseURL = c.BaseURL
		}
		var err error
		client, err = bedrock.NewClient(context.Background(), cfg, clientOpts...)
		if err != nil {
			return nil, fmt.Errorf("configure Bedrock credentials/region: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported PMQ_AI_PROVIDER %q (use openai, openrouter, or bedrock)", provider)
	}
	return &sdkChatProvider{client: client, provider: provider, models: models}, nil
}

type sdkChatProvider struct {
	client   openai.Client
	provider string
	models   map[string]string
}

func (p *sdkChatProvider) Complete(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
	var req ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	publicModel := req.Model
	model, ok := p.models[publicModel]
	if !ok {
		return nil, &aiError{400, "unsupported_model", "Model is not enabled by the PMQ server configuration"}
	}
	req.Model = model
	if req.MaxTokens == nil {
		limit := 1024
		req.MaxTokens = &limit
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var response json.RawMessage
	if err := p.client.Execute(ctx, http.MethodPost, "/chat/completions", bytes.NewReader(encoded), &response); err != nil {
		return nil, err
	}
	var upstream ChatResponse
	if err := json.Unmarshal(response, &upstream); err != nil {
		return nil, errors.New("provider returned an invalid Chat Completions response")
	}
	if upstream.Object != "chat.completion" || len(upstream.Choices) == 0 {
		return nil, errors.New("provider returned an incomplete Chat Completions response")
	}
	for _, choice := range upstream.Choices {
		if choice.Message.Role != "assistant" || choice.Message.Content == "" {
			return nil, errors.New("provider returned a non-text Chat Completions response")
		}
	}
	upstream.Model = publicModel
	return json.Marshal(upstream)
}

func (s *Server) ai(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/_pmq/ai/chat/completions" {
		fail(w, 404, "not_found", "AI endpoint not found")
		return
	}
	if r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "Method not allowed")
		return
	}
	if s.aiProvider == nil {
		fail(w, 503, "ai_unavailable", "AI provider is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAIRequest)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			fail(w, 413, "request_too_large", "AI request body exceeds 1 MiB")
		} else {
			fail(w, 400, "invalid_request", "Could not read request body")
		}
		return
	}
	var req ChatRequest
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		fail(w, 400, "invalid_request", "Expected a valid Chat Completions JSON request")
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		fail(w, 400, "invalid_request", "Request must contain one JSON value")
		return
	}
	if strings.TrimSpace(req.Model) == "" || len(req.Messages) == 0 || req.Stream {
		fail(w, 400, "invalid_request", "model and messages are required; streaming is not supported")
		return
	}
	for _, m := range req.Messages {
		if (m.Role != "system" && m.Role != "user" && m.Role != "assistant") || m.Content == "" {
			fail(w, 400, "invalid_request", "messages must contain nonempty text with system, user, or assistant roles")
			return
		}
	}
	if req.MaxTokens != nil && (*req.MaxTokens < 1 || *req.MaxTokens > 32768) {
		fail(w, 400, "invalid_request", "max_tokens must be between 1 and 32768")
		return
	}
	if req.Temperature != nil && (*req.Temperature < 0 || *req.Temperature > 2) {
		fail(w, 400, "invalid_request", "temperature must be between 0 and 2")
		return
	}
	if req.TopP != nil && (*req.TopP < 0 || *req.TopP > 1) {
		fail(w, 400, "invalid_request", "top_p must be between 0 and 1")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.aiTimeout)
	defer cancel()
	response, err := s.aiProvider.Complete(ctx, json.RawMessage(b))
	if err != nil {
		var ae *aiError
		if errors.As(err, &ae) {
			fail(w, ae.status, ae.code, ae.msg)
			return
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			fail(w, 504, "provider_timeout", "AI provider timed out")
			return
		}
		fail(w, 502, "provider_error", "AI provider request failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(response)
}
