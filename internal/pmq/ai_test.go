package pmq

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type chatProviderFunc func(context.Context, json.RawMessage) (json.RawMessage, error)

func (f chatProviderFunc) Complete(ctx context.Context, b json.RawMessage) (json.RawMessage, error) {
	return f(ctx, b)
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
		{`{"model":"x","messages":[{"role":"user","content":"hi"}],"stream":true}`, 400},
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
			p, err := newAIProvider(AIConfig{Provider: provider, APIKey: key, BaseURL: ts.URL + "/v1", Models: map[string]string{"friendly": "model-x"}})
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
	p, err := newAIProvider(AIConfig{Provider: "openai", APIKey: "test-key", BaseURL: ts.URL + "/v1", Models: map[string]string{"x": "model-x"}})
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
	p, err := newAIProvider(AIConfig{Provider: "bedrock", AWSRegion: "us-east-1", BaseURL: ts.URL + "/openai/v1", BedrockModels: map[string]string{"fast": "openai.gpt-oss-20b-1:0"}})
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
