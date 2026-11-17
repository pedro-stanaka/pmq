package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pmq-dev/pmq/internal/pmq"
)

// TestFullLifecycleSmoke drives the CLI and real HTTP handlers with an
// isolated PMQ server and a deterministic mock provider.
func TestFullLifecycleSmoke(t *testing.T) {
	providerCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls++
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("provider path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"mock","object":"chat.completion","created":1,"model":"mock-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello from mock"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`)
	}))
	defer provider.Close()
	s, err := pmq.New(pmq.Config{
		Listen: "127.0.0.1:8080", DataDir: t.TempDir(), Domain: "apps.localhost", Token: "smoke-token", Dev: true,
		AI: pmq.AIConfig{OpenAIKey: "mock-key", OpenAIBaseURL: provider.URL + "/v1", DefaultModel: "fast", Models: map[string]pmq.ModelBinding{"fast": {Provider: "openai", ModelID: "mock-model"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	server := httptest.NewServer(s)
	defer server.Close()
	t.Setenv("PMQ_SERVER", server.URL)
	t.Setenv("PMQ_DEPLOY_TOKEN", "smoke-token")

	// The vanilla app's build step copies its static source into a clean output.
	build := t.TempDir()
	if err := os.CopyFS(build, os.DirFS(filepath.Join("..", "..", "examples", "hello-world"))); err != nil {
		t.Fatalf("build app: %v", err)
	}
	if err := run([]string{"deploy", build, "--name", "smoke"}); err != nil {
		t.Fatalf("deploy v1: %v", err)
	}
	call := func(method, path string, body io.Reader) (int, []byte) {
		t.Helper()
		r, err := http.NewRequest(method, server.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		r.Host = "smoke.apps.localhost"
		if method == "POST" || method == "PUT" || method == "DELETE" {
			r.Header.Set("Origin", "http://"+r.Host)
		}
		resp, err := server.Client().Do(r)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, data
	}
	expect := func(method, path, body string, status int, want string) []byte {
		t.Helper()
		code, data := call(method, path, strings.NewReader(body))
		if code != status || !strings.Contains(string(data), want) {
			t.Fatalf("%s %s: status %d, body %q; expected %d containing %q", method, path, code, data, status, want)
		}
		return data
	}
	expect("GET", "/", "", 200, "Hello, PMQ")
	created := expect("POST", "/_pmq/db/notes", `{"text":"persistent note"}`, 201, "persistent note")
	var doc struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created, &doc); err != nil || doc.ID == "" {
		t.Fatalf("create document response %s: %v", created, err)
	}
	expect("GET", "/_pmq/db/notes/"+doc.ID, "", 200, "persistent note")
	expect("PUT", "/_pmq/storage/hello.txt", "object bytes", 201, "")
	expect("GET", "/_pmq/storage/hello.txt", "", 200, "object bytes")
	request, err := http.NewRequest("GET", server.URL+"/_pmq/storage/hello.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "smoke.apps.localhost"
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("read object ETag: %v", err)
	}
	etag := response.Header.Get("ETag")
	response.Body.Close()
	if etag == "" {
		t.Fatal("filesystem object response has no ETag")
	}
	request.Header.Set("If-None-Match", etag)
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional GET: %d", response.StatusCode)
	}
	expect("POST", "/_pmq/ai/chat/completions", `{"messages":[{"role":"user","content":"hi"}]}`, 200, "hello from mock")
	if providerCalls != 1 {
		t.Fatalf("provider calls = %d", providerCalls)
	}
	if err := os.WriteFile(filepath.Join(build, "index.html"), []byte("second release"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"deploy", build, "--name", "smoke"}); err != nil {
		t.Fatalf("deploy v2: %v", err)
	}
	expect("GET", "/", "", 200, "second release")
	expect("GET", "/_pmq/db/notes/"+doc.ID, "", 200, "persistent note")
	expect("GET", "/_pmq/storage/hello.txt", "", 200, "object bytes")
	if err := run([]string{"rollback", "smoke"}); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	expect("GET", "/", "", 200, "Hello, PMQ")
	expect("GET", "/_pmq/db/notes/"+doc.ID, "", 200, "persistent note")
	expect("GET", "/_pmq/storage/hello.txt", "", 200, "object bytes")
	// The deployed JS should be available as an actual asset, not only index.html.
	code, script := call("GET", "/app.js", bytes.NewReader(nil))
	if code != 200 || !bytes.Contains(script, []byte("/_pmq/")) {
		t.Fatal(fmt.Sprintf("missing deployed app.js: status %d", code))
	}
}
