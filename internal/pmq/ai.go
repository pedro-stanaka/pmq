package pmq

import (
	"bufio"
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
const maxAIStreamLine = 256 << 10

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string        `json:"model,omitempty"`
	Provider    string        `json:"provider,omitempty"`
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

type ChatProvider interface {
	Complete(context.Context, json.RawMessage) (json.RawMessage, error)
}

type StreamingChatProvider interface {
	Stream(context.Context, json.RawMessage, func(json.RawMessage) error) error
}

type ModelBinding struct {
	Provider  string `json:"provider"`
	ModelID   string `json:"model_id"`
	Streaming bool   `json:"streaming,omitempty"`
}

type AIConfig struct {
	Models            map[string]ModelBinding
	DefaultModel      string
	OpenAIKey         string
	OpenRouterKey     string
	AWSRegion         string
	OpenAIBaseURL     string
	OpenRouterBaseURL string
	BedrockBaseURL    string
	HTTPClient        *http.Client
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
	if len(c.Models) == 0 && c.DefaultModel == "" {
		return nil, nil
	}
	if len(c.Models) == 0 {
		return nil, errors.New("PMQ_AI_MODELS must configure model aliases")
	}
	if c.DefaultModel == "" {
		return nil, errors.New("PMQ_AI_DEFAULT_MODEL is required")
	}
	if _, ok := c.Models[c.DefaultModel]; !ok {
		return nil, fmt.Errorf("default model alias %q is not configured", c.DefaultModel)
	}
	clients := make(map[string]openai.Client)
	models := make(map[string]ModelBinding, len(c.Models))
	for alias, binding := range c.Models {
		if strings.TrimSpace(alias) == "" || strings.TrimSpace(binding.ModelID) == "" {
			return nil, fmt.Errorf("AI model alias %q has an empty alias or upstream model ID", alias)
		}
		provider := strings.ToLower(strings.TrimSpace(binding.Provider))
		if provider != "openai" && provider != "openrouter" && provider != "bedrock" {
			return nil, fmt.Errorf("model alias %q uses unsupported provider %q", alias, binding.Provider)
		}
		binding.Provider = provider
		models[alias] = binding
		if _, exists := clients[provider]; exists {
			continue
		}
		client, err := newAIClient(provider, c)
		if err != nil {
			return nil, fmt.Errorf("configure %s provider for model alias %q: %w", provider, alias, err)
		}
		clients[provider] = client
	}
	return &sdkAIProvider{clients: clients, models: models, defaultModel: c.DefaultModel}, nil
}

func newAIClient(provider string, c AIConfig) (openai.Client, error) {
	opts := []option.RequestOption{option.WithHTTPClient(boundedAIHTTPClient(c.HTTPClient))}
	switch provider {
	case "openai", "openrouter":
		key, base := c.OpenAIKey, c.OpenAIBaseURL
		if provider == "openrouter" {
			key, base = c.OpenRouterKey, c.OpenRouterBaseURL
			if base == "" {
				base = "https://openrouter.ai/api/v1"
			}
		} else if base == "" {
			base = "https://api.openai.com/v1"
		}
		if key == "" {
			return openai.Client{}, fmt.Errorf("%s API key is required", provider)
		}
		if !validAIBaseURL(base) {
			return openai.Client{}, errors.New("provider base URL must use HTTPS without userinfo, query, or fragment (loopback HTTP is allowed for tests)")
		}
		opts = append(opts, option.WithBaseURL(base), option.WithAPIKey(key))
		return openai.NewClient(opts...), nil
	case "bedrock":
		cfg := bedrock.Config{AWSRegion: c.AWSRegion, Endpoint: bedrock.EndpointRuntime}
		if c.BedrockBaseURL != "" {
			if !validAIBaseURL(c.BedrockBaseURL) {
				return openai.Client{}, errors.New("Bedrock base URL must use HTTPS without userinfo, query, or fragment (loopback HTTP is allowed for tests)")
			}
			cfg.BaseURL = c.BedrockBaseURL
		}
		return bedrock.NewClient(context.Background(), cfg, opts...)
	default:
		return openai.Client{}, fmt.Errorf("unsupported provider %q", provider)
	}
}

type sdkAIProvider struct {
	clients      map[string]openai.Client
	models       map[string]ModelBinding
	defaultModel string
}

func (p *sdkAIProvider) resolve(req *ChatRequest) (string, ModelBinding, error) {
	alias := req.Model
	if alias == "" {
		alias = p.defaultModel
	}
	binding, ok := p.models[alias]
	if !ok {
		return "", ModelBinding{}, &aiError{400, "unsupported_model", "Model alias is not enabled by the PMQ server configuration"}
	}
	if req.Provider != "" && !strings.EqualFold(req.Provider, binding.Provider) {
		return "", ModelBinding{}, &aiError{400, "provider_mismatch", "Requested provider does not match the server-configured model alias"}
	}
	req.Model = alias
	return alias, binding, nil
}

func (p *sdkAIProvider) Complete(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
	var req ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	alias, binding, err := p.resolve(&req)
	if err != nil {
		return nil, err
	}
	client := p.clients[strings.ToLower(binding.Provider)]
	req.Model = binding.ModelID
	req.Provider = ""
	req.Stream = false
	if req.MaxTokens == nil {
		limit := 1024
		req.MaxTokens = &limit
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var response json.RawMessage
	if err := client.Execute(ctx, http.MethodPost, "/chat/completions", bytes.NewReader(encoded), &response); err != nil {
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
	upstream.Model = alias
	return json.Marshal(upstream)
}

func (p *sdkAIProvider) Stream(ctx context.Context, body json.RawMessage, emit func(json.RawMessage) error) error {
	var req ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return err
	}
	alias, binding, err := p.resolve(&req)
	if err != nil {
		return err
	}
	if strings.EqualFold(binding.Provider, "bedrock") && !binding.Streaming {
		return &aiError{400, "streaming_unsupported", "Streaming is not enabled for this Bedrock model alias"}
	}
	client := p.clients[strings.ToLower(binding.Provider)]
	req.Model, req.Provider, req.Stream = binding.ModelID, "", true
	if req.MaxTokens == nil {
		limit := 1024
		req.MaxTokens = &limit
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		return err
	}
	var upstream *http.Response
	if err := client.Execute(ctx, http.MethodPost, "/chat/completions", bytes.NewReader(encoded), &upstream); err != nil {
		return err
	}
	if upstream == nil || upstream.Body == nil {
		return errors.New("provider returned an empty stream")
	}
	defer upstream.Body.Close()
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		return fmt.Errorf("provider returned HTTP %d", upstream.StatusCode)
	}
	return readChatSSE(ctx, upstream.Body, alias, emit)
}

type chatStreamChoice struct {
	Index int `json:"index"`
	Delta struct {
		Role    string `json:"role,omitempty"`
		Content string `json:"content,omitempty"`
	} `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

type chatStreamChunk struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []chatStreamChoice `json:"choices"`
	Usage   *ChatUsage         `json:"usage,omitempty"`
}

func readChatSSE(ctx context.Context, upstream io.Reader, alias string, emit func(json.RawMessage) error) error {
	scanner := bufio.NewScanner(upstream)
	scanner.Buffer(make([]byte, 4096), maxAIStreamLine)
	var data strings.Builder
	flush := func() (bool, error) {
		if data.Len() == 0 {
			return false, nil
		}
		value := strings.TrimSuffix(data.String(), "\n")
		data.Reset()
		if value == "[DONE]" {
			return true, nil
		}
		var chunk chatStreamChunk
		if err := json.Unmarshal([]byte(value), &chunk); err != nil {
			return false, errors.New("provider returned an invalid stream event")
		}
		if chunk.Object != "chat.completion.chunk" {
			return false, errors.New("provider returned an unsupported stream event")
		}
		chunk.Model = alias
		encoded, err := json.Marshal(chunk)
		if err != nil {
			return false, err
		}
		if err := emit(encoded); err != nil {
			return false, err
		}
		return false, nil
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		if line == "" {
			done, err := flush()
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			part := strings.TrimPrefix(line, "data:")
			part = strings.TrimPrefix(part, " ")
			if data.Len()+len(part)+1 > maxAIStreamLine {
				return errors.New("provider stream event exceeds size limit")
			}
			data.WriteString(part)
			data.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	_, err := flush()
	return err
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
	if strings.TrimSpace(req.Model) == "" && req.Model != "" || len(req.Messages) == 0 {
		fail(w, 400, "invalid_request", "messages are required and model must be a valid alias")
		return
	}
	if req.Provider != "" && req.Model == "" {
		fail(w, 400, "provider_mismatch", "Provider override requires an explicit configured model alias")
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
	if req.Stream {
		s.aiStream(w, ctx, json.RawMessage(b))
		return
	}
	response, err := s.aiProvider.Complete(ctx, json.RawMessage(b))
	if err != nil {
		s.aiFail(w, err, ctx)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(response)
}

func (s *Server) aiStream(w http.ResponseWriter, ctx context.Context, body json.RawMessage) {
	streamer, ok := s.aiProvider.(StreamingChatProvider)
	if !ok {
		fail(w, 501, "streaming_unsupported", "Configured provider does not support streaming")
		return
	}
	started := false
	emit := func(chunk json.RawMessage) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache, no-transform")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(http.StatusOK)
			started = true
		}
		if _, err := w.Write(append(append([]byte("data: "), chunk...), '\n', '\n')); err != nil {
			return err
		}
		if err := http.NewResponseController(w).Flush(); err != nil {
			return err
		}
		return nil
	}
	if err := streamer.Stream(ctx, body, emit); err != nil {
		if !started {
			s.aiFail(w, err, ctx)
			return
		}
		payload, _ := json.Marshal(map[string]any{"error": map[string]string{"code": "provider_error", "message": "AI provider stream failed"}})
		_, _ = w.Write(append(append([]byte("event: error\ndata: "), payload...), '\n', '\n'))
		_ = http.NewResponseController(w).Flush()
		return
	}
	if !started {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	_ = http.NewResponseController(w).Flush()
}

func (s *Server) aiFail(w http.ResponseWriter, err error, ctx context.Context) {
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
}
