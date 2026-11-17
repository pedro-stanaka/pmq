package pmq

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type chatProviderFunc func(context.Context, json.RawMessage) (json.RawMessage, error)

func (f chatProviderFunc) Complete(ctx context.Context, b json.RawMessage) (json.RawMessage, error) {
	return f(ctx, b)
}

type streamChatProvider struct{ chatProviderFunc }

func (p streamChatProvider) Stream(ctx context.Context, _ json.RawMessage, emit func(json.RawMessage) error) error {
	return emit(json.RawMessage(`{"id":"chunk","object":"chat.completion.chunk","created":1,"model":"fast","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`))
}

const chatResponse = `{"id":"chatcmpl-test","object":"chat.completion","created":1,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"provider_secret":"never forward"}`

func aiFixture(t *testing.T, provider ChatProvider) *Server {
	t.Helper()
	s, err := New(Config{Listen: "127.0.0.1:8080", DataDir: t.TempDir(), Domain: "apps.localhost", Token: "secret", Dev: true, AIProvider: provider})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	upload(t, s, "chat", "app")
	return s
}

func TestAIChatCompletionsContract(t *testing.T) {
	var called bool
	s := aiFixture(t, chatProviderFunc(func(_ context.Context, b json.RawMessage) (json.RawMessage, error) {
		called = true
		var got ChatRequest
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if got.Model != "alias" || len(got.Messages) != 1 || got.Messages[0].Content != "hello" {
			t.Fatalf("unexpected request: %+v", got)
		}
		return json.RawMessage(chatResponse), nil
	}))
	w := req(s, http.MethodPost, "chat.apps.localhost", "/_pmq/ai/chat/completions", strings.NewReader(`{"model":"alias","messages":[{"role":"user","content":"hello"}]}`), true)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" || !called || !strings.Contains(w.Body.String(), `"chat.completion"`) {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body)
	}
}

func TestAIHTTPStreamingFrames(t *testing.T) {
	p := streamChatProvider{chatProviderFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(chatResponse), nil
	})}
	s := aiFixture(t, p)
	w := req(s, http.MethodPost, "chat.apps.localhost", "/_pmq/ai/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}],"stream":true}`), true)
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/event-stream" || !w.Flushed || !strings.Contains(w.Body.String(), `data: {"id":"chunk"`) || !strings.HasSuffix(w.Body.String(), "data: [DONE]\n\n") {
		t.Fatalf("unexpected SSE response: status=%d headers=%v body=%q", w.Code, w.Header(), w.Body.String())
	}
}

func TestModelAliasProviderRoutingAndOverride(t *testing.T) {
	var openAIModel, routerModel string
	openAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body ChatRequest
		_ = json.Unmarshal(b, &body)
		openAIModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatResponse)
	}))
	defer openAI.Close()
	router := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body ChatRequest
		_ = json.Unmarshal(b, &body)
		routerModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatResponse)
	}))
	defer router.Close()
	p, err := newAIProvider(AIConfig{
		DefaultModel: "fast", OpenAIKey: "openai-key", OpenAIBaseURL: openAI.URL + "/v1", OpenRouterKey: "router-key", OpenRouterBaseURL: router.URL + "/v1",
		Models: map[string]ModelBinding{"fast": {Provider: "openai", ModelID: "gpt-fast"}, "router": {Provider: "openrouter", ModelID: "vendor/router-model"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Complete(context.Background(), json.RawMessage(`{"messages":[{"role":"user","content":"use default"}]}`)); err != nil {
		t.Fatal(err)
	}
	if openAIModel != "gpt-fast" {
		t.Fatalf("default alias routed model %q", openAIModel)
	}
	if _, err = p.Complete(context.Background(), json.RawMessage(`{"model":"router","messages":[{"role":"user","content":"override"}]}`)); err != nil {
		t.Fatal(err)
	}
	if routerModel != "vendor/router-model" {
		t.Fatalf("override alias routed model %q", routerModel)
	}
	_, err = p.Complete(context.Background(), json.RawMessage(`{"model":"router","provider":"openai","messages":[{"role":"user","content":"blocked"}]}`))
	var ae *aiError
	if !errors.As(err, &ae) || ae.code != "provider_mismatch" || openAIModel != "gpt-fast" || routerModel != "vendor/router-model" {
		t.Fatalf("provider override was not rejected before dispatch: %v", err)
	}
}

func TestAIErrorsAndValidation(t *testing.T) {
	s := aiFixture(t, chatProviderFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("secret provider error")
	}))
	for _, tc := range []struct {
		body   string
		status int
	}{
		{"{", 400},
		{`{"model":"x","messages":[]}`, 400},
		{`{"model":"x","messages":[{"role":"user","content":"hi"}],"unexpected":1}`, 400},
	} {
		w := req(s, http.MethodPost, "chat.apps.localhost", "/_pmq/ai/chat/completions", strings.NewReader(tc.body), true)
		if w.Code != tc.status {
			t.Fatalf("%s: got %d %s", tc.body, w.Code, w.Body)
		}
	}
	w := req(s, http.MethodPost, "chat.apps.localhost", "/_pmq/ai/chat/completions", strings.NewReader(`{"model":"x","messages":[{"role":"user","content":"hi"}]}`), true)
	if w.Code != 502 || strings.Contains(w.Body.String(), "secret provider error") {
		t.Fatalf("provider error leaked: %d %s", w.Code, w.Body)
	}
	if w := req(s, http.MethodGet, "chat.apps.localhost", "/_pmq/ai/chat/completions", nil, true); w.Code != 405 {
		t.Fatalf("method: %d", w.Code)
	}
}

func TestAIProviderSDKTransport(t *testing.T) {
	for _, provider := range []string{"openai", "openrouter"} {
		t.Run(provider, func(t *testing.T) {
			var auth string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
					t.Errorf("unexpected upstream route %s %s", r.Method, r.URL.Path)
				}
				if b, _ := io.ReadAll(r.Body); !strings.Contains(string(b), `"model":"model-x"`) || !strings.Contains(string(b), `"max_tokens":1024`) {
					t.Errorf("unexpected upstream body: %s", b)
				}
				auth = r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, chatResponse)
			}))
			defer ts.Close()
			key := "test-key"
			cfg := AIConfig{Models: map[string]ModelBinding{"friendly": {Provider: provider, ModelID: "model-x"}}, DefaultModel: "friendly"}
			if provider == "openrouter" {
				cfg.OpenRouterKey, cfg.OpenRouterBaseURL = key, ts.URL+"/v1"
			} else {
				cfg.OpenAIKey, cfg.OpenAIBaseURL = key, ts.URL+"/v1"
			}
			p, err := newAIProvider(cfg)
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Complete(context.Background(), json.RawMessage(`{"model":"friendly","messages":[{"role":"user","content":"hi"}]}`))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), "chat.completion") || strings.Contains(string(got), "provider_secret") || !strings.Contains(string(got), `"model":"friendly"`) || auth != "Bearer test-key" {
				t.Fatalf("response=%s auth=%q", got, auth)
			}
			_, err = p.Complete(context.Background(), json.RawMessage(`{"model":"unlisted","messages":[{"role":"user","content":"hi"}]}`))
			var ae *aiError
			if !errors.As(err, &ae) || ae.code != "unsupported_model" {
				t.Fatalf("unexpected model error: %v", err)
			}
		})
	}
}

func TestAIProviderResponseLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", maxAIResponse+100))
	}))
	defer ts.Close()
	p, err := newAIProvider(AIConfig{OpenAIKey: "test-key", OpenAIBaseURL: ts.URL + "/v1", DefaultModel: "x", Models: map[string]ModelBinding{"x": {Provider: "openai", ModelID: "model-x"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Complete(context.Background(), json.RawMessage(`{"model":"x","messages":[{"role":"user","content":"hi"}]}`))
	if !errors.Is(err, errAIResponseTooLarge) {
		t.Fatalf("expected response size error, got %v", err)
	}
}

func TestBedrockSDKAuthAndModelAllowlist(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("AWS_REGION", "us-east-1")
	var auth, body string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openai/v1/chat/completions" {
			t.Errorf("unexpected Bedrock path %q", r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatResponse)
	}))
	defer ts.Close()
	p, err := newAIProvider(AIConfig{AWSRegion: "us-east-1", BedrockBaseURL: ts.URL + "/openai/v1", DefaultModel: "fast", Models: map[string]ModelBinding{"fast": {Provider: "bedrock", ModelID: "openai.gpt-oss-20b-1:0", Streaming: true}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Complete(context.Background(), json.RawMessage(`{"model":"fast","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") || !strings.Contains(body, `"model":"openai.gpt-oss-20b-1:0"`) || !strings.Contains(body, `"max_tokens":1024`) || !strings.Contains(string(got), `"model":"fast"`) {
		t.Fatalf("auth=%q body=%s response=%s", auth, body, got)
	}
	_, err = p.Complete(context.Background(), json.RawMessage(`{"model":"unlisted","messages":[{"role":"user","content":"hi"}]}`))
	var ae *aiError
	if !errors.As(err, &ae) || ae.code != "unsupported_model" {
		t.Fatalf("unexpected model error: %v", err)
	}
}

func TestStreamingProviders(t *testing.T) {
	for _, provider := range []string{"openai", "openrouter", "bedrock"} {
		t.Run(provider, func(t *testing.T) {
			if provider == "bedrock" {
				t.Setenv("AWS_ACCESS_KEY_ID", "test-access")
				t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
				t.Setenv("AWS_REGION", "us-east-1")
			}
			var upstreamBody, auth string
			path := "/v1/chat/completions"
			if provider == "bedrock" {
				path = "/openai/v1/chat/completions"
			}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != path {
					t.Errorf("unexpected route: %s", r.URL.Path)
				}
				b, _ := io.ReadAll(r.Body)
				upstreamBody, auth = string(b), r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"id\":\"chunk-1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"upstream\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n")
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				_, _ = io.WriteString(w, "data: {\"id\":\"chunk-2\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"upstream\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n")
				_, _ = io.WriteString(w, "data: {\"id\":\"chunk-3\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"upstream\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer ts.Close()
			cfg := AIConfig{DefaultModel: "friendly", Models: map[string]ModelBinding{"friendly": {Provider: provider, ModelID: "upstream", Streaming: true}}}
			switch provider {
			case "openai":
				cfg.OpenAIKey, cfg.OpenAIBaseURL = "test-key", ts.URL+"/v1"
			case "openrouter":
				cfg.OpenRouterKey, cfg.OpenRouterBaseURL = "test-key", ts.URL+"/v1"
			case "bedrock":
				cfg.AWSRegion, cfg.BedrockBaseURL = "us-east-1", ts.URL+"/openai/v1"
			}
			p, err := newAIProvider(cfg)
			if err != nil {
				t.Fatal(err)
			}
			streamer := p.(StreamingChatProvider)
			var events []json.RawMessage
			err = streamer.Stream(context.Background(), json.RawMessage(`{"messages":[{"role":"user","content":"hi"}],"stream":true}`), func(b json.RawMessage) error {
				events = append(events, append(json.RawMessage(nil), b...))
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 3 || !strings.Contains(string(events[1]), `"content":"hello"`) || !strings.Contains(string(events[1]), `"model":"friendly"`) {
				t.Fatalf("unexpected stream: %s", events)
			}
			if !strings.Contains(upstreamBody, `"model":"upstream"`) || !strings.Contains(upstreamBody, `"stream":true`) || !strings.Contains(upstreamBody, `"max_tokens":1024`) {
				t.Fatalf("unexpected request: %s", upstreamBody)
			}
			if provider == "bedrock" && !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
				t.Fatalf("Bedrock request was not signed: %s", auth)
			}
		})
	}
}

func TestBedrockStreamingRequiresConfiguredCapability(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("AWS_REGION", "us-east-1")
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; _, _ = io.WriteString(w, "data: [DONE]\n\n") }))
	defer ts.Close()
	p, err := newAIProvider(AIConfig{AWSRegion: "us-east-1", BedrockBaseURL: ts.URL + "/openai/v1", DefaultModel: "fast", Models: map[string]ModelBinding{"fast": {Provider: "bedrock", ModelID: "model-id"}}})
	if err != nil {
		t.Fatal(err)
	}
	err = p.(StreamingChatProvider).Stream(context.Background(), json.RawMessage(`{"messages":[{"role":"user","content":"hi"}],"stream":true}`), func(json.RawMessage) error { return nil })
	var ae *aiError
	if !errors.As(err, &ae) || ae.code != "streaming_unsupported" || calls != 0 {
		t.Fatalf("unexpected capability result err=%v calls=%d", err, calls)
	}
}

func TestStreamingClientDisconnectCancelsUpstream(t *testing.T) {
	upstreamCanceled := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chunk\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first\"},\"finish_reason\":null}]}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
		close(upstreamCanceled)
	}))
	defer ts.Close()
	p, err := newAIProvider(AIConfig{OpenAIKey: "test-key", OpenAIBaseURL: ts.URL + "/v1", DefaultModel: "fast", Models: map[string]ModelBinding{"fast": {Provider: "openai", ModelID: "model"}}})
	if err != nil {
		t.Fatal(err)
	}
	s := aiFixture(t, p)
	appServer := httptest.NewServer(s)
	defer appServer.Close()
	r, err := http.NewRequest(http.MethodPost, appServer.URL+"/_pmq/ai/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "chat.apps.localhost"
	r.Header.Set("Origin", "http://chat.apps.localhost")
	resp, err := appServer.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || !strings.Contains(line, `"content":"first"`) {
		t.Fatalf("first token not delivered: %q %v", line, err)
	}
	_ = resp.Body.Close()
	select {
	case <-upstreamCanceled:
	case <-time.After(3 * time.Second):
		t.Fatal("client disconnect did not cancel upstream stream")
	}
}

func TestAIUnauthorizedAndTimeout(t *testing.T) {
	// The normal server auth boundary rejects API calls before invoking a provider.
	s := fixture(t, false)
	upload(t, s, "chat", "app")
	if w := req(s, http.MethodPost, "chat.apps.localhost", "/_pmq/ai/chat/completions", strings.NewReader(`{}`), false); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	s = aiFixture(t, chatProviderFunc(func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	s.aiTimeout = 1
	w := req(s, http.MethodPost, "chat.apps.localhost", "/_pmq/ai/chat/completions", strings.NewReader(`{"model":"x","messages":[{"role":"user","content":"hi"}]}`), true)
	if w.Code != 504 {
		t.Fatalf("timeout: %d %s", w.Code, w.Body)
	}
}
