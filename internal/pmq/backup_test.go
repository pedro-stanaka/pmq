package pmq

import (
	"io"
	"os"
	"strings"
	"testing"
)

// TestOfflineBackupRestoreTwoApps exercises the documented stop-copy-restore
// procedure without relying on a personal server or external storage.
func TestOfflineBackupRestoreTwoApps(t *testing.T) {
	original, backup, restored := t.TempDir(), t.TempDir(), t.TempDir()
	config := Config{Listen: "127.0.0.1:8080", DataDir: original, Domain: "apps.localhost", Token: "secret", Dev: true}
	s, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range []string{"one", "two"} {
		upload(t, s, app, app+"-old")
		upload(t, s, app, app+"-current")
		host := app + ".apps.localhost"
		w := req(s, "POST", host, "/_pmq/db/notes", strings.NewReader(`{"text":"`+app+`-document"}`), true)
		if w.Code != 201 {
			t.Fatalf("create %s document: %d %s", app, w.Code, w.Body)
		}
		w = req(s, "PUT", host, "/_pmq/storage/file.txt", strings.NewReader(app+"-object"), true)
		if w.Code != 201 {
			t.Fatalf("upload %s object: %d %s", app, w.Code, w.Body)
		}
	}
	// Close SQLite and storage before copying the entire data directory.
	s.Close()
	if err := os.CopyFS(backup, os.DirFS(original)); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if err := os.CopyFS(restored, os.DirFS(backup)); err != nil {
		t.Fatalf("restore: %v", err)
	}
	config.DataDir = restored
	s, err = New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, app := range []string{"one", "two"} {
		host := app + ".apps.localhost"
		for _, tc := range []struct{ path, want string }{
			{"/", app + "-current"},
			{"/_pmq/db/notes", app + "-document"},
			{"/_pmq/storage/file.txt", app + "-object"},
		} {
			w := req(s, "GET", host, tc.path, nil, false)
			if w.Code != 200 || !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("restore %s %s: %d %s", app, tc.path, w.Code, w.Body)
			}
		}
		w := req(s, "GET", "localhost", "/_pmq/admin/releases/"+app, nil, true)
		body, _ := io.ReadAll(w.Body)
		if w.Code != 200 || strings.Count(string(body), `"id"`) != 2 {
			t.Fatalf("restore %s release history: %d %s", app, w.Code, body)
		}
	}
}
