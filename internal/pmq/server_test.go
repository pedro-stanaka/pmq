package pmq

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func fixture(t *testing.T, dev bool) *Server {
	t.Helper()
	s, e := New(Config{Listen: "127.0.0.1:8080", DataDir: t.TempDir(), Domain: "apps.localhost", Token: "secret", Dev: dev, IdentityHeader: "X-PMQ-User", TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return s
}
func archive(t *testing.T, name, contents string, typ byte) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	if typ == 0 {
		typ = tar.TypeReg
	}
	h := &tar.Header{Name: name, Typeflag: typ, Mode: 0600}
	if typ == tar.TypeReg {
		h.Size = int64(len(contents))
	}
	if e := tw.WriteHeader(h); e != nil {
		t.Fatal(e)
	}
	if typ == tar.TypeReg {
		if _, e := tw.Write([]byte(contents)); e != nil {
			t.Fatal(e)
		}
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}
func req(s *Server, method, host, p string, body io.Reader, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+host+p, body)
	r.Host = host
	if auth {
		r.Header.Set("Authorization", "Bearer secret")
		r.Header.Set("X-PMQ-User", "alice")
		r.Header.Set("Origin", "https://"+host)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func upload(t *testing.T, s *Server, name, contents string) {
	t.Helper()
	w := req(s, "POST", "localhost", "/_pmq/admin/deploy/"+name, bytes.NewReader(archive(t, "index.html", contents, 0)), true)
	if w.Code != 201 {
		t.Fatalf("deploy %d %s", w.Code, w.Body)
	}
}
func TestNamesAndHosts(t *testing.T) {
	for _, s := range []string{"", "admin", "a.b", "-foo", "a/b", "foo-", "FOO"} {
		if ValidName(s) {
			t.Error(s)
		}
	}
	if !ValidName("hello-world") {
		t.Fatal("valid name rejected")
	}
	for _, h := range []string{"x.evil.apps.localhost", "evilapps.localhost", "admin.apps.localhost", "x.apps.localhost:bad:port"} {
		if _, e := HostApp(h, "apps.localhost"); e == nil {
			t.Error(h)
		}
	}
	if a, e := HostApp("hello-world.apps.localhost:8080", "apps.localhost"); e != nil || a != "hello-world" {
		t.Fatal(a, e)
	}
}
func TestDeploymentRoutingAndSafety(t *testing.T) {
	s := fixture(t, true)
	upload(t, s, "hello", "first")
	host := "hello.apps.localhost"
	for _, p := range []string{"/", "/route"} {
		w := req(s, "GET", host, p, nil, false)
		if w.Code != 200 || w.Body.String() != "first" {
			t.Fatal(p, w.Code, w.Body)
		}
	}
	if w := req(s, "GET", "unknown.apps.localhost", "/", nil, false); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := req(s, "GET", host, "/%2e%2e/secret", nil, false); w.Code != 400 {
		t.Fatal(w.Code)
	}
	for _, tc := range []struct {
		n   string
		typ byte
	}{{"../index.html", tar.TypeReg}, {"/index.html", tar.TypeReg}, {"index.html", tar.TypeSymlink}} {
		w := req(s, "POST", "localhost", "/_pmq/admin/deploy/hello", bytes.NewReader(archive(t, tc.n, "bad", tc.typ)), true)
		if w.Code == 201 {
			t.Fatal(tc)
		}
		if w = req(s, "GET", host, "/", nil, false); w.Body.String() != "first" {
			t.Fatal("failed upload changed release")
		}
	}
	upload(t, s, "hello", "second")
	if w := req(s, "GET", host, "/", nil, false); w.Body.String() != "second" {
		t.Fatal(w.Body)
	}
}
func TestAuthAndCRUDIsolation(t *testing.T) {
	s := fixture(t, false)
	upload(t, s, "one", "one")
	upload(t, s, "two", "two")
	host := "one.apps.localhost"
	if w := req(s, "GET", host, "/", nil, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("GET", "http://"+host+"/", nil)
	r.RemoteAddr = "192.0.2.10:1000"
	r.Header.Set("X-PMQ-User", "forged")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := req(s, "POST", host, "/_pmq/db/tasks", strings.NewReader(`{"title":"hi"}`), false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("POST", "http://"+host+"/_pmq/db/tasks", strings.NewReader(`{}`))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-PMQ-User", "alice")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	// Requests from a configured proxy carry identity and the HTTPS browser origin.
	call := func(method, h, p, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+h+p, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("X-PMQ-User", "alice")
		r.Header.Set("Origin", "https://"+h)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	w = call("POST", host, "/_pmq/db/tasks", `{"title":"hi"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var d document
	if e := json.Unmarshal(w.Body.Bytes(), &d); e != nil {
		t.Fatal(e)
	}
	p := "/_pmq/db/tasks/" + d.ID
	if w = call("GET", host, p, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = call("GET", "two.apps.localhost", p, ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w = call("PUT", host, p, `{"done":true}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	upload(t, s, "one", "new")
	if w = call("GET", host, p, ""); w.Code != 200 {
		t.Fatal("data lost", w.Code)
	}
	if w = call("GET", host, "/_pmq/db/tasks", ""); w.Code != 200 || !strings.Contains(w.Body.String(), d.ID) {
		t.Fatal(w.Code, w.Body)
	}
	if w = call("DELETE", host, p, ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w = call("GET", host, p, ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w = call("POST", host, "/_pmq/db/tasks", "{"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w = call("POST", host, "/_pmq/db/tasks", strings.Repeat("x", MaxJSON+1)); w.Code != 413 {
		t.Fatal(w.Code)
	}
	if w = req(s, "GET", "localhost", "/_pmq/admin/apps", nil, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
func TestConcurrentWrites(t *testing.T) {
	s := fixture(t, true)
	upload(t, s, "one", "ok")
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := req(s, "POST", "one.apps.localhost", "/_pmq/db/tasks", strings.NewReader(fmt.Sprintf(`{"n":%d}`, i)), true)
			if w.Code != 201 {
				t.Errorf("%d %s", w.Code, w.Body)
			}
		}(i)
	}
	wg.Wait()
	w := req(s, "GET", "one.apps.localhost", "/_pmq/db/tasks", nil, false)
	var v struct{ Documents []document }
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil || len(v.Documents) != 30 {
		t.Fatal(e, len(v.Documents))
	}
	if _, e := os.Stat(filepath.Join(s.cfg.DataDir, "state", "one", "data.sqlite")); e != nil {
		t.Fatal(e)
	}
}
func TestConfigurationFailsClosed(t *testing.T) {
	if _, e := New(Config{Listen: "0.0.0.0:8080", DataDir: t.TempDir(), Domain: "apps.localhost", Token: "x", Dev: true}); e == nil {
		t.Fatal("nonloopback dev")
	}
	if _, e := New(Config{Listen: "127.0.0.1:8080", DataDir: t.TempDir(), Domain: "apps.localhost", Token: "x"}); e == nil {
		t.Fatal("missing proxy")
	}
	if _, e := New(Config{Listen: "127.0.0.1:8080", DataDir: t.TempDir(), Domain: "apps.localhost", Dev: true}); e == nil {
		t.Fatal("missing token")
	}
}

func TestHTTPIntegration(t *testing.T) {
	s := fixture(t, false)
	ts := httptest.NewServer(s)
	defer ts.Close()
	client := ts.Client()
	// The trusted test server connection originates at loopback.
	call := func(method, host, p string, body io.Reader, identity bool, admin bool) *http.Response {
		t.Helper()
		r, e := http.NewRequest(method, ts.URL+p, body)
		if e != nil {
			t.Fatal(e)
		}
		r.Host = host
		if identity {
			r.Header.Set("X-PMQ-User", "test-user")
			r.Header.Set("Origin", "https://"+host)
		}
		if admin {
			r.Header.Set("Authorization", "Bearer secret")
		}
		response, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		return response
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	root := filepath.Join("..", "..", "examples", "hello-world")
	for _, name := range []string{"index.html", "app.js", "style.css"} {
		b, e := os.ReadFile(filepath.Join(root, name))
		if e != nil {
			t.Fatal(e)
		}
		if e = tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(b))}); e != nil {
			t.Fatal(e)
		}
		if _, e = tw.Write(b); e != nil {
			t.Fatal(e)
		}
	}
	if e := tw.Close(); e != nil {
		t.Fatal(e)
	}
	if e := gz.Close(); e != nil {
		t.Fatal(e)
	}
	resp := call("POST", "localhost", "/_pmq/admin/deploy/hello-world", &buf, false, true)
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatal(resp.Status)
	}
	upload(t, s, "other", "other")
	host := "hello-world.apps.localhost"
	resp = call("GET", host, "/", nil, true, false)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "Hello, PMQ") {
		t.Fatal(resp.Status, string(b))
	}
	resp = call("POST", host, "/_pmq/db/notes", strings.NewReader(`{"text":"hello"}`), true, false)
	var d document
	e := json.NewDecoder(resp.Body).Decode(&d)
	resp.Body.Close()
	if e != nil || resp.StatusCode != 201 {
		t.Fatal(e, resp.Status)
	}
	p := "/_pmq/db/notes/" + d.ID
	resp = call("GET", host, p, nil, true, false)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	resp = call("GET", host, p, nil, false, false)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal(resp.Status)
	}
	resp = call("GET", "other.apps.localhost", p, nil, true, false)
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal(resp.Status)
	}
}

func TestInterruptedAndOversizedDeploy(t *testing.T) {
	s := fixture(t, true)
	upload(t, s, "hello", "old")
	valid := archive(t, "index.html", "new", 0)
	truncated := valid[:len(valid)-8]
	w := req(s, "POST", "localhost", "/_pmq/admin/deploy/hello", bytes.NewReader(truncated), true)
	if w.Code == 201 {
		t.Fatal("truncated archive activated")
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "index.html", Mode: 0600, Size: MaxUpload + 1}); err != nil {
		t.Fatal(err)
	}
	// The oversized declared entry is rejected before its contents are read.
	_ = tw.Flush()
	_ = gz.Close()
	w = req(s, "POST", "localhost", "/_pmq/admin/deploy/hello", bytes.NewReader(buf.Bytes()), true)
	if w.Code == 201 {
		t.Fatal("oversized archive activated")
	}
	w = req(s, "GET", "hello.apps.localhost", "/", nil, false)
	if w.Code != 200 || w.Body.String() != "old" {
		t.Fatal("previous release changed", w.Code, w.Body)
	}
	w = req(s, "GET", "hello.apps.localhost", "/_pmq/storage/object", nil, false)
	if w.Code != 404 {
		t.Fatal("reserved route served as app content", w.Code)
	}
}
