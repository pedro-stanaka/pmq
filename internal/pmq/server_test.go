package pmq

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
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

func TestReleaseHistoryRollbackAndPrune(t *testing.T) {
	s := fixture(t, true)
	upload(t, s, "release-app", "v1")
	status := req(s, "GET", "localhost", "/_pmq/admin/status/release-app", nil, true)
	if status.Code != 200 {
		t.Fatal(status.Code, status.Body)
	}
	var first struct {
		ActiveRelease string `json:"activeRelease"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	created := req(s, "POST", "release-app.apps.localhost", "/_pmq/db/notes", strings.NewReader(`{"title":"persistent"}`), true)
	if created.Code != 201 {
		t.Fatalf("create persistent document: %d %s", created.Code, created.Body)
	}
	upload(t, s, "release-app", "v2")
	active := req(s, "GET", "localhost", "/_pmq/admin/status/release-app", nil, true)
	var second struct {
		ActiveRelease string `json:"activeRelease"`
	}
	_ = json.Unmarshal(active.Body.Bytes(), &second)
	if second.ActiveRelease == first.ActiveRelease {
		t.Fatal("deploy did not create new release")
	}
	pruneLatest := req(s, "POST", "localhost", "/_pmq/admin/prune/release-app?keep=1", nil, true)
	if pruneLatest.Code != 200 {
		t.Fatalf("prune latest: %d %s", pruneLatest.Code, pruneLatest.Body)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.DataDir, "apps", "release-app", "releases", first.ActiveRelease)); err != nil {
		t.Fatalf("prune removed rollback target: %v", err)
	}
	bad := req(s, "POST", "localhost", "/_pmq/admin/rollback/release-app", strings.NewReader(`{"release":"00000000000000000000000000000000"}`), true)
	if bad.Code != 400 {
		t.Fatal(bad.Code, bad.Body)
	}
	unchanged := req(s, "GET", "localhost", "/_pmq/admin/status/release-app", nil, true)
	var still struct {
		ActiveRelease string `json:"activeRelease"`
	}
	_ = json.Unmarshal(unchanged.Body.Bytes(), &still)
	if still.ActiveRelease != second.ActiveRelease {
		t.Fatal("invalid rollback changed active release")
	}
	rb := req(s, "POST", "localhost", "/_pmq/admin/rollback/release-app", strings.NewReader(`{"release":"`+first.ActiveRelease+`"}`), true)
	if rb.Code != 200 {
		t.Fatal(rb.Code, rb.Body)
	}
	page := req(s, "GET", "release-app.apps.localhost", "/", nil, true)
	if page.Code != 200 || page.Body.String() != "v1" {
		t.Fatalf("rollback response %d %q", page.Code, page.Body.String())
	}
	docs := req(s, "GET", "release-app.apps.localhost", "/_pmq/db/notes", nil, true)
	if docs.Code != 200 || !strings.Contains(docs.Body.String(), `"title":"persistent"`) {
		t.Fatalf("persistent document changed across rollback: %d %s", docs.Code, docs.Body)
	}
	if noauth := req(s, "GET", "localhost", "/_pmq/admin/releases/release-app", nil, false); noauth.Code != 401 {
		t.Fatal("release history unauthenticated", noauth.Code)
	}
	pr := req(s, "POST", "localhost", "/_pmq/admin/prune/release-app?keep=1", nil, true)
	if pr.Code != 200 {
		t.Fatal(pr.Code, pr.Body)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.DataDir, "apps", "release-app", "releases", first.ActiveRelease)); err != nil {
		t.Fatal("active release pruned", err)
	}
}

func TestConcurrentDeployAndRollback(t *testing.T) {
	s := fixture(t, true)
	upload(t, s, "race-app", "v1")
	hist := req(s, "GET", "localhost", "/_pmq/admin/releases/race-app", nil, true)
	var history struct {
		Releases []releaseInfo `json:"releases"`
	}
	if err := json.Unmarshal(hist.Body.Bytes(), &history); err != nil || len(history.Releases) != 1 {
		t.Fatal(err, hist.Body)
	}
	old := history.Releases[0].ID
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				upload(t, s, "race-app", fmt.Sprintf("v%d", i+2))
			} else {
				w := req(s, "POST", "localhost", "/_pmq/admin/rollback/race-app", strings.NewReader(`{"release":"`+old+`"}`), true)
				if w.Code != 200 {
					t.Errorf("rollback %d: %s", w.Code, w.Body)
				}
			}
		}(i)
	}
	wg.Wait()
	active := req(s, "GET", "localhost", "/_pmq/admin/status/race-app", nil, true)
	if active.Code != 200 {
		t.Fatal(active.Code, active.Body)
	}
}

func TestInterruptedActivationLeavesPointerUntouched(t *testing.T) {
	s := fixture(t, true)
	upload(t, s, "activate-app", "known")
	base := filepath.Join(s.cfg.DataDir, "apps", "activate-app")
	current := filepath.Join(base, "current")
	saved := filepath.Join(base, "saved-current")
	if err := os.Rename(current, saved); err != nil {
		t.Fatal(err)
	}
	// A directory at the pointer path makes atomic rename fail after the temporary pointer is written.
	if err := os.Mkdir(current, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.activate("activate-app", strings.Repeat("a", 32)); err == nil {
		t.Fatal("expected activation failure")
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".current-") {
			t.Fatalf("temporary pointer leaked: %s", e.Name())
		}
	}
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, current); err != nil {
		t.Fatal(err)
	}
	got, err := s.releaseID("activate-app")
	if err != nil || got == "" {
		t.Fatalf("previous pointer was not recoverable: %q %v", got, err)
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

func TestStorageRangeAndConditionalRequests(t *testing.T) {
	s := fixture(t, true)
	upload(t, s, "range-app", "app")
	host := "range-app.apps.localhost"
	call := func(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
		r.Host = host
		r.Header.Set("Origin", "http://"+host)
		for name, value := range headers {
			r.Header.Set(name, value)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := call("PUT", "/_pmq/storage/range.txt", "0123456789", nil); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	for _, tc := range []struct {
		rangeValue string
		want       string
	}{
		{"bytes=2-5", "2345"},
		{"bytes=7-", "789"},
		{"bytes=-3", "789"},
		{"bytes=-99", "0123456789"},
	} {
		w := call("GET", "/_pmq/storage/range.txt", "", map[string]string{"Range": tc.rangeValue})
		if w.Code != http.StatusPartialContent || w.Body.String() != tc.want || w.Header().Get("Content-Length") != fmt.Sprint(len(tc.want)) {
			t.Errorf("range %q: status=%d body=%q length=%s", tc.rangeValue, w.Code, w.Body.String(), w.Header().Get("Content-Length"))
		}
		if !strings.HasPrefix(w.Header().Get("Content-Range"), "bytes ") {
			t.Errorf("range %q missing Content-Range: %q", tc.rangeValue, w.Header().Get("Content-Range"))
		}
	}
	if w := call("GET", "/_pmq/storage/range.txt", "", map[string]string{"Range": "bytes=20-30"}); w.Code != 416 || w.Header().Get("Content-Range") != "bytes */10" {
		t.Fatalf("unsatisfiable range: %d %v %s", w.Code, w.Header(), w.Body)
	}
	if w := call("HEAD", "/_pmq/storage/range.txt", "", map[string]string{"Range": "bytes=0-1"}); w.Code != 200 || w.Header().Get("Content-Length") != "10" || w.Body.Len() != 0 {
		t.Fatalf("range HEAD: %d %v %s", w.Code, w.Header(), w.Body)
	}
	if w := call("GET", "/_pmq/storage/range.txt", "", map[string]string{"If-None-Match": "*"}); w.Code != 304 {
		t.Fatalf("If-None-Match: %d %s", w.Code, w.Body)
	}
	if w := call("GET", "/_pmq/storage/range.txt", "", map[string]string{"If-Match": `"wrong"`}); w.Code != 412 {
		t.Fatalf("If-Match: %d %s", w.Code, w.Body)
	}
	meta := call("HEAD", "/_pmq/storage/range.txt", "", nil)
	if modified := meta.Header().Get("Last-Modified"); modified == "" {
		t.Fatal("missing Last-Modified")
	} else if w := call("GET", "/_pmq/storage/range.txt", "", map[string]string{"If-Modified-Since": modified}); w.Code != 304 {
		t.Fatalf("If-Modified-Since: %d %s", w.Code, w.Body)
	}
	if w := call("GET", "/_pmq/storage/range.txt", "", map[string]string{"Range": "bytes=0-1", "If-Range": `"different"`}); w.Code != 200 || w.Body.String() != "0123456789" {
		t.Fatalf("If-Range mismatch should return full body: %d %q", w.Code, w.Body.String())
	}
}

func TestStorageLargeTransfersStreamInChunks(t *testing.T) {
	s := fixture(t, true)
	upload(t, s, "stream-app", "app")
	host := "stream-app.apps.localhost"
	const size = 16 << 20
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	request := httptest.NewRequest("PUT", "http://"+host+"/_pmq/storage/large.bin", &generatedBody{remaining: size})
	request.Host = host
	request.Header.Set("Origin", "http://"+host)
	w := &discardResponse{header: make(http.Header)}
	s.ServeHTTP(w, request)
	if w.status != http.StatusCreated {
		t.Fatalf("stream upload: %d", w.status)
	}
	request = httptest.NewRequest("GET", "http://"+host+"/_pmq/storage/large.bin", nil)
	request.Host = host
	w = &discardResponse{header: make(http.Header)}
	s.ServeHTTP(w, request)
	if w.status != http.StatusOK || w.written != size || w.maxWrite > 1<<20 {
		t.Fatalf("stream download: status=%d bytes=%d maxWrite=%d", w.status, w.written, w.maxWrite)
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated >= uint64(size/2) {
		t.Fatalf("large transfer allocated %d bytes for %d-byte object; expected bounded buffering", allocated, size)
	}
}

func TestStorageUploadCancellationMetric(t *testing.T) {
	s := fixture(t, true)
	upload(t, s, "cancel-app", "app")
	r, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest("PUT", "http://cancel-app.apps.localhost/_pmq/storage/cancelled", strings.NewReader("partial")).WithContext(r)
	request.Host = "cancel-app.apps.localhost"
	request.Header.Set("Origin", "http://cancel-app.apps.localhost")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, request)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("canceled upload response: %d %s", w.Code, w.Body)
	}
	if w := req(s, "PUT", "cancel-app.apps.localhost", "/_pmq/storage/readable", strings.NewReader("body"), true); w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body)
	}
	downloadCtx, stopDownload := context.WithCancel(context.Background())
	downloading := httptest.NewRequest("GET", "http://cancel-app.apps.localhost/_pmq/storage/readable", nil).WithContext(downloadCtx)
	downloading.Host = "cancel-app.apps.localhost"
	writer := &cancelResponse{header: make(http.Header), cancel: stopDownload}
	s.ServeHTTP(writer, downloading)
	metrics := httptest.NewRequest("GET", "http://localhost/_pmq/admin/metrics", nil)
	metrics.Header.Set("Authorization", "Bearer secret")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, metrics)
	if !strings.Contains(w.Body.String(), `pmq_storage_transfer_cancellations_total{operation="upload"} 1`) || !strings.Contains(w.Body.String(), `pmq_storage_transfer_cancellations_total{operation="download"} 1`) {
		t.Fatalf("cancellation metric missing: %s", w.Body)
	}
}

type cancelResponse struct {
	header http.Header
	cancel context.CancelFunc
}

func (w *cancelResponse) Header() http.Header { return w.header }
func (w *cancelResponse) WriteHeader(int)     {}
func (w *cancelResponse) Write([]byte) (int, error) {
	w.cancel()
	return 0, io.ErrClosedPipe
}

type generatedBody struct{ remaining int64 }

func (b *generatedBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > b.remaining {
		n = b.remaining
	}
	for i := 0; i < int(n); i++ {
		p[i] = 'x'
	}
	b.remaining -= n
	return int(n), nil
}
func (b *generatedBody) Close() error { return nil }

type discardResponse struct {
	header   http.Header
	status   int
	written  int64
	maxWrite int
}

func (w *discardResponse) Header() http.Header    { return w.header }
func (w *discardResponse) WriteHeader(status int) { w.status = status }
func (w *discardResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if len(p) > w.maxWrite {
		w.maxWrite = len(p)
	}
	w.written += int64(len(p))
	return len(p), nil
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

func scrapeMetrics(t *testing.T, s *Server) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "http://localhost/_pmq/admin/metrics", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("metrics scrape: %d %s", w.Code, w.Body)
	}
	return w.Body.String()
}

func TestUnifiedMetricsDocumentAndStorageOperations(t *testing.T) {
	s := fixture(t, true)
	upload(t, s, "one", "one")
	upload(t, s, "two", "two")
	host := "one.apps.localhost"
	created := req(s, http.MethodPost, host, "/_pmq/db/private-collection", strings.NewReader(`{"v":1}`), true)
	if created.Code != http.StatusCreated {
		t.Fatal(created.Code, created.Body)
	}
	var doc document
	if err := json.Unmarshal(created.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	path := "/_pmq/db/private-collection/" + doc.ID
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, path, ""},
		{http.MethodGet, "/_pmq/db/private-collection", ""},
		{http.MethodPut, path, `{"v":2}`},
		{http.MethodDelete, path, ""},
		{http.MethodGet, path, ""}, // not found
	} {
		w := req(s, tc.method, host, tc.path, strings.NewReader(tc.body), true)
		if tc.method == http.MethodGet && tc.path == path {
			// The second read is intentionally absent after DELETE.
			if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
				t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body)
			}
		} else if w.Code >= 500 {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body)
		}
	}
	if w := req(s, http.MethodPut, host, "/_pmq/storage/object.bin", strings.NewReader("payload"), true); w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body)
	}
	if w := req(s, http.MethodGet, host, "/_pmq/storage/object.bin", nil, false); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body)
	}
	if w := req(s, http.MethodGet, host, "/_pmq/storage", nil, false); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body)
	}
	if w := req(s, http.MethodDelete, host, "/_pmq/storage/object.bin", nil, true); w.Code != http.StatusNoContent {
		t.Fatal(w.Code, w.Body)
	}
	// A directory at the filesystem provider's object path makes replacement fail
	// after the request reaches the provider, exercising its bounded error outcome.
	encoded := base64.RawURLEncoding.EncodeToString([]byte("provider-error"))
	if err := os.MkdirAll(filepath.Join(s.cfg.DataDir, "objects", "apps", "one", encoded), 0700); err != nil {
		t.Fatal(err)
	}
	if w := req(s, http.MethodPut, host, "/_pmq/storage/provider-error", strings.NewReader("x"), true); w.Code != http.StatusInternalServerError {
		t.Fatalf("expected provider error, got %d %s", w.Code, w.Body)
	}
	metrics := scrapeMetrics(t, s)
	for _, want := range []string{
		`pmq_sqlite_document_operations_total{operation="create",outcome="success"} 1`,
		`pmq_sqlite_document_operations_total{operation="list",outcome="success"} 1`,
		`pmq_sqlite_document_operations_total{operation="get",outcome="success"} 1`,
		`pmq_sqlite_document_operations_total{operation="update",outcome="success"} 1`,
		`pmq_sqlite_document_operations_total{operation="delete",outcome="success"} 1`,
		`pmq_sqlite_document_operations_total{operation="get",outcome="not_found"} 1`,
		`pmq_sqlite_document_rows_returned_total{operation="list"} 1`,
		`pmq_http_request_duration_seconds_bucket{method="POST",route="/_pmq/db/{collection}"`,
		`pmq_storage_http_operations_total{operation="upload",outcome="success"} 1`,
		`pmq_storage_http_operations_total{operation="download",outcome="success"} 1`,
		`pmq_storage_http_operations_total{operation="list",outcome="success"} 1`,
		`pmq_storage_http_operations_total{operation="delete",outcome="success"} 1`,
		`pmq_storage_http_operations_total{operation="upload",outcome="provider_error"} 1`,
		`objstore_bucket_operations_total{bucket="pmq",operation="upload"}`,
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
	for _, private := range []string{"private-collection", doc.ID, "one.apps.localhost", "two.apps.localhost", "provider-error", "object.bin"} {
		if strings.Contains(metrics, private) {
			t.Errorf("metrics leaked dynamic value %q", private)
		}
	}
	if w := req(s, http.MethodGet, host, "/_pmq/metrics", nil, false); w.Code != http.StatusNotFound {
		t.Fatalf("public metrics path should not be exposed: %d", w.Code)
	}
}

func TestMetricsConcurrentAcrossApplications(t *testing.T) {
	s := fixture(t, true)
	upload(t, s, "first", "first")
	upload(t, s, "second", "second")
	const writesPerApp = 24
	var wg sync.WaitGroup
	for _, app := range []string{"first", "second"} {
		app := app
		for i := 0; i < writesPerApp; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				host := app + ".apps.localhost"
				w := req(s, http.MethodPost, host, "/_pmq/db/tasks", strings.NewReader(fmt.Sprintf(`{"n":%d}`, i)), true)
				if w.Code != http.StatusCreated {
					t.Errorf("%s create: %d %s", app, w.Code, w.Body)
				}
			}(i)
		}
	}
	wg.Wait()
	metrics := scrapeMetrics(t, s)
	want := fmt.Sprintf(`pmq_sqlite_document_operations_total{operation="create",outcome="success"} %d`, 2*writesPerApp)
	if !strings.Contains(metrics, want) {
		t.Fatalf("cross-app writes not aggregated in shared registry; missing %q", want)
	}
	for _, private := range []string{"first.apps.localhost", "second.apps.localhost"} {
		if strings.Contains(metrics, private) {
			t.Errorf("metrics leaked app namespace %q", private)
		}
	}
}

func TestSQLiteBusyMetric(t *testing.T) {
	s := fixture(t, true)
	upload(t, s, "busy", "app")
	host := "busy.apps.localhost"
	if w := req(s, http.MethodGet, host, "/_pmq/db/tasks", nil, false); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body)
	}
	pmqDB := s.dbs["busy"]
	if _, err := pmqDB.Exec("PRAGMA busy_timeout=25"); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(s.cfg.DataDir, "state", "busy", "data.sqlite")
	lockDB, err := sql.Open("sqlite", filename+"?_pragma=busy_timeout(25)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer lockDB.Close()
	if _, err = lockDB.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer lockDB.Exec("ROLLBACK")
	w := req(s, http.MethodPost, host, "/_pmq/db/tasks", strings.NewReader(`{"blocked":true}`), true)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("busy insert should fail with 500, got %d %s", w.Code, w.Body)
	}
	metrics := scrapeMetrics(t, s)
	for _, want := range []string{
		`pmq_sqlite_document_operations_total{operation="create",outcome="busy"} 1`,
		`pmq_sqlite_document_errors_total{error="busy",operation="create"} 1`,
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("busy metrics missing %q", want)
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
