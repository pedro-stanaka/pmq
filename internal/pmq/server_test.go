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
	"net/url"
	"os"
	"path/filepath"
	"sort"
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
	if w := req(s, "PUT", host, "/_pmq/storage/secret.txt", strings.NewReader("no"), false); w.Code != 401 {
		t.Fatalf("unauthenticated storage mutation: %d", w.Code)
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

func TestStorageHTTPCRUDAndIsolation(t *testing.T) {
	s := fixture(t, true)
	upload(t, s, "one", "one")
	upload(t, s, "two", "two")
	host := "one.apps.localhost"
	call := func(method, h, p string, body io.Reader, origin bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+h+p, body)
		r.Host = h
		if origin {
			r.Header.Set("Origin", "http://"+h)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := call("PUT", host, "/_pmq/storage/folder%2Fnote.txt", strings.NewReader("binary\x00data"), false); w.Code != 403 {
		t.Fatalf("csrf %d", w.Code)
	}
	w := call("PUT", host, "/_pmq/storage/folder%2Fnote.txt", strings.NewReader("binary\x00data"), true)
	if w.Code != 201 {
		t.Fatalf("put %d %s", w.Code, w.Body)
	}
	if w = call("DELETE", host, "/_pmq/storage/folder%2Fnote.txt", nil, false); w.Code != 403 {
		t.Fatalf("cross-origin delete %d", w.Code)
	}
	w = call("GET", host, "/_pmq/storage/folder%2Fnote.txt", nil, false)
	if w.Code != 200 || w.Body.String() != "binary\x00data" {
		t.Fatalf("get %d %q", w.Code, w.Body.String())
	}
	w = call("HEAD", host, "/_pmq/storage/folder%2Fnote.txt", nil, false)
	if w.Code != 200 || w.Header().Get("Content-Length") != "11" || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("head %d %#v", w.Code, w.Header())
	}
	if _, err := http.ParseTime(w.Header().Get("Last-Modified")); err != nil {
		t.Fatalf("Last-Modified is not an HTTP date: %q (%v)", w.Header().Get("Last-Modified"), err)
	}
	w = call("GET", "two.apps.localhost", "/_pmq/storage/folder%2Fnote.txt", nil, false)
	if w.Code != 404 {
		t.Fatalf("cross-app %d", w.Code)
	}
	w = call("GET", host, "/_pmq/storage", nil, false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "folder/note.txt") {
		t.Fatalf("list %d %s", w.Code, w.Body)
	}
	for _, p := range []string{"/_pmq/storage/%2e%2e/secret", "/_pmq/storage/a//b", "/_pmq/storage/%00", "/_pmq/storage/%FF"} {
		w = call("GET", host, p, nil, false)
		if w.Code != 400 {
			t.Errorf("invalid %s: %d", p, w.Code)
		}
	}
	w = call("GET", host, "/_pmq/storage/missing", nil, false)
	if w.Code != 404 || w.Header().Get("Content-Length") != "" || w.Header().Get("Last-Modified") != "" || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("missing %d", w.Code)
	}
	w = call("DELETE", host, "/_pmq/storage/folder%2Fnote.txt", nil, true)
	if w.Code != 204 {
		t.Fatalf("delete %d %s", w.Code, w.Body)
	}
	w = call("GET", host, "/_pmq/storage/folder%2Fnote.txt", nil, false)
	if w.Code != 404 {
		t.Fatalf("post-delete %d", w.Code)
	}
	for _, item := range []struct {
		key  string
		body []byte
	}{{"empty.bin", nil}, {"binary.bin", []byte{0, 1, 255, 0}}} {
		w = call("PUT", host, "/_pmq/storage/"+item.key, bytes.NewReader(item.body), true)
		if w.Code != 201 {
			t.Fatalf("put %s: %d %s", item.key, w.Code, w.Body)
		}
		w = call("GET", host, "/_pmq/storage/"+item.key, nil, false)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), item.body) {
			t.Fatalf("get %s: %d %v", item.key, w.Code, w.Body.Bytes())
		}
	}
	for _, key := range []string{"a_", "a-", "a.", "a", "a~", "a/child"} {
		w = call("PUT", host, "/_pmq/storage/"+url.PathEscape(key), strings.NewReader(key), true)
		if w.Code != 201 {
			t.Fatalf("put %q: %d %s", key, w.Code, w.Body)
		}
	}
	seen := []string{}
	cursor := ""
	for {
		p := "/_pmq/storage?limit=2"
		if cursor != "" {
			p += "&cursor=" + url.QueryEscape(cursor)
		}
		w = call("GET", host, p, nil, false)
		if w.Code != 200 {
			t.Fatalf("page %d %s", w.Code, w.Body)
		}
		var page struct {
			Objects []struct {
				Key string `json:"key"`
			} `json:"objects"`
			Next string `json:"nextCursor"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil {
			t.Fatal(e)
		}
		for _, o := range page.Objects {
			seen = append(seen, o.Key)
		}
		cursor = page.Next
		if cursor == "" {
			break
		}
	}
	want := append([]string(nil), seen...)
	sort.Strings(want)
	if strings.Join(seen, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("pagination order got %v want %v", seen, want)
	}
	expected := []string{"a", "a-", "a.", "a/child", "a_", "a~", "binary.bin", "empty.bin"}
	if strings.Join(seen, "\x00") != strings.Join(expected, "\x00") {
		t.Fatalf("pagination entries got %v want %v", seen, expected)
	}
	w = call("PUT", host, "/_pmq/storage/large.bin", strings.NewReader(strings.Repeat("x", MaxObject+1)), true)
	if w.Code != 413 {
		t.Fatalf("large %d %s", w.Code, w.Body)
	}
}

func TestStorageMetricsScrapeRestrictedAndLowCardinality(t *testing.T) {
	s := fixture(t, true)
	upload(t, s, "one", "x")
	call := func(method, path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost"+path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "/_pmq/admin/metrics", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call("GET", "/_pmq/admin/metrics", "secret"); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	callObj := func(method, path string, body io.Reader) {
		r := httptest.NewRequest(method, "http://one.apps.localhost"+path, body)
		r.Host = "one.apps.localhost"
		r.Header.Set("Origin", "http://one.apps.localhost")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
	}
	callObj("PUT", "/_pmq/storage/private-key.txt", strings.NewReader("abc"))
	callObj("GET", "/_pmq/storage/private-key.txt", nil)
	callObj("GET", "/_pmq/storage/no-such-key", nil)
	w := call("GET", "/_pmq/admin/metrics", "secret")
	body := w.Body.String()
	for _, metric := range []string{"objstore_bucket_operations_total{bucket=\"pmq\",operation=\"upload\"}", "objstore_bucket_operation_failures_total{bucket=\"pmq\",operation=\"get\"}", "objstore_bucket_operation_duration_seconds_bucket{bucket=\"pmq\",operation=\"upload\"", "objstore_bucket_operation_transferred_bytes_bucket{bucket=\"pmq\",operation=\"upload\""} {
		if !strings.Contains(body, metric) {
			t.Errorf("missing metric %q", metric)
		}
	}
	for _, secret := range []string{"private-key.txt", "apps/one", "one.apps.localhost"} {
		if strings.Contains(body, secret) {
			t.Errorf("high cardinality label leaked %q", secret)
		}
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
