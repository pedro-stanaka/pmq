package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pmq-dev/pmq/internal/pmq"
)

// Exercise the commands documented by the deployment skill against PMQ itself.
func TestDeploySkillWorkflow(t *testing.T) {
	t.Setenv("PMQ_DEPLOY_TOKEN", "test-only")
	s, err := pmq.New(pmq.Config{Listen: "127.0.0.1:8080", DataDir: t.TempDir(), Domain: "apps.localhost", Token: "test-only", Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := httptest.NewServer(s)
	defer ts.Close()
	t.Setenv("PMQ_SERVER", ts.URL)
	dir := t.TempDir()
	index := filepath.Join(dir, "index.html")
	if err := os.WriteFile(index, []byte("first release"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"deploy", dir, "--name", "demo"}); err != nil {
		t.Fatal(err)
	}
	checkPage := func(want string) {
		t.Helper()
		r, err := http.NewRequest("GET", ts.URL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Host = "demo.apps.localhost"
		resp, err := ts.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != 200 || string(b) != want {
			t.Fatalf("app response: status %d, body %q, error %v", resp.StatusCode, b, err)
		}
	}
	checkPage("first release")
	for _, args := range [][]string{{"list"}, {"status", "demo"}, {"releases", "demo"}} {
		if err := run(args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if err := os.WriteFile(index, []byte("second release"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"deploy", dir, "--name", "demo"}); err != nil {
		t.Fatal(err)
	}
	checkPage("second release")
	if err := run([]string{"rollback", "demo"}); err != nil {
		t.Fatal(err)
	}
	checkPage("first release")

	t.Setenv("PMQ_DEPLOY_TOKEN", "bad-token")
	if err := run([]string{"status", "demo"}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("bad token accepted: %v", err)
	}
	t.Setenv("PMQ_DEPLOY_TOKEN", "test-only")
	if err := run([]string{"deploy", filepath.Join(dir, "missing"), "--name", "demo"}); err == nil {
		t.Fatal("missing build accepted")
	}
	if err := run([]string{"deploy", t.TempDir(), "--name", "demo"}); err == nil {
		t.Fatal("build without index.html accepted")
	}
	checkPage("first release")
	r, _ := http.NewRequest("GET", ts.URL+"/", nil)
	r.Host = "demo.extra.apps.localhost"
	resp, err := ts.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unsupported hostname: %d", resp.StatusCode)
	}
}
