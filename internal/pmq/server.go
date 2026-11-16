package pmq

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const MaxUpload = 32 << 20
const MaxJSON = 1 << 20

var nameRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var idRE = regexp.MustCompile(`^[a-f0-9]{32}$`)
var reserved = map[string]bool{"www": true, "api": true, "admin": true, "_pmq": true, "pmq": true}

func ValidName(s string) bool {
	return len(s) > 0 && len(s) <= 63 && nameRE.MatchString(s) && !reserved[s]
}
func HostApp(host, domain string) (string, error) {
	if strings.Contains(host, ":") {
		var err error
		host, _, err = net.SplitHostPort(host)
		if err != nil {
			return "", errors.New("invalid host")
		}
	}
	host = strings.ToLower(host)
	domain = strings.ToLower(domain)
	suffix := "." + domain
	if !strings.HasSuffix(host, suffix) {
		return "", errors.New("invalid host")
	}
	app := strings.TrimSuffix(host, suffix)
	if !ValidName(app) {
		return "", errors.New("invalid host")
	}
	return app, nil
}

func validDomain(domain string) bool {
	if len(domain) > 253 || domain == "" {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || !nameRE.MatchString(label) {
			return false
		}
	}
	return true
}

func validHeader(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	return true
}

type Config struct {
	Listen, DataDir, Domain, Token, IdentityHeader string
	Dev                                            bool
	TrustedProxies                                 []netip.Prefix
}
type Server struct {
	cfg Config
	mu  sync.Mutex
	dbs map[string]*sql.DB
}

func New(c Config) (*Server, error) {
	if c.Token == "" {
		return nil, errors.New("deploy token is required")
	}
	if c.DataDir == "" || c.Domain == "" {
		return nil, errors.New("data directory and domain are required")
	}
	if !validDomain(c.Domain) {
		return nil, errors.New("invalid base domain")
	}
	if c.Dev {
		h, _, err := net.SplitHostPort(c.Listen)
		if err != nil || net.ParseIP(h) == nil || !net.ParseIP(h).IsLoopback() {
			return nil, errors.New("development mode requires a loopback listen address")
		}
	} else if len(c.TrustedProxies) == 0 || !validHeader(c.IdentityHeader) {
		return nil, errors.New("production requires trusted proxies and an identity header")
	}
	if err := os.MkdirAll(c.DataDir, 0700); err != nil {
		return nil, err
	}
	for _, d := range []string{"apps", "state"} {
		if err := os.MkdirAll(filepath.Join(c.DataDir, d), 0700); err != nil {
			return nil, err
		}
	}
	version := filepath.Join(c.DataDir, "VERSION")
	b, err := os.ReadFile(version)
	if errors.Is(err, os.ErrNotExist) {
		err = os.WriteFile(version, []byte("1\n"), 0600)
	} else if err == nil && string(b) != "1\n" {
		return nil, errors.New("unsupported data layout version")
	}
	if err != nil {
		return nil, err
	}
	return &Server{cfg: c, dbs: make(map[string]*sql.DB)}, nil
}
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, db := range s.dbs {
		_ = db.Close()
	}
}
func fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) authorized(r *http.Request) bool {
	if s.cfg.Dev {
		return true
	}
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(h)
	if err != nil {
		return false
	}
	trusted := false
	for _, p := range s.cfg.TrustedProxies {
		if p.Contains(ip.Unmap()) {
			trusted = true
			break
		}
	}
	v := r.Header.Values(s.cfg.IdentityHeader)
	return trusted && len(v) == 1 && strings.TrimSpace(v[0]) != "" && !strings.ContainsAny(v[0], ",\r\n")
}
func sameOrigin(r *http.Request, dev bool) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	return err == nil && (u.Scheme == "https" || (dev && u.Scheme == "http")) && strings.EqualFold(u.Host, r.Host) && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path == "/_pmq/health" {
		if r.Method != http.MethodGet {
			fail(w, 405, "method_not_allowed", "Method not allowed")
			return
		}
		reply(w, 200, map[string]string{"status": "ok"})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_pmq/admin/") {
		s.admin(w, r)
		return
	}
	app, err := HostApp(r.Host, s.cfg.Domain)
	if err != nil {
		fail(w, 404, "not_found", "Application not found")
		return
	}
	release, err := s.current(app)
	if err != nil {
		fail(w, 404, "not_found", "Application not found")
		return
	}
	if !s.authorized(r) {
		fail(w, 401, "unauthorized", "Authentication required")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && !sameOrigin(r, s.cfg.Dev) {
		fail(w, 403, "csrf_rejected", "A matching Origin is required")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_pmq/") || r.URL.Path == "/_pmq" {
		if strings.HasPrefix(r.URL.Path, "/_pmq/db/") {
			s.documents(w, r, app)
		} else {
			fail(w, 404, "not_found", "Endpoint not found")
		}
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		fail(w, 405, "method_not_allowed", "Method not allowed")
		return
	}
	s.static(w, r, release)
}
func (s *Server) current(app string) (string, error) {
	b, err := os.ReadFile(filepath.Join(s.cfg.DataDir, "apps", app, "current"))
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if !idRE.MatchString(id) {
		return "", errors.New("invalid release")
	}
	p := filepath.Join(s.cfg.DataDir, "apps", app, "releases", id)
	st, err := os.Stat(p)
	if err != nil || !st.IsDir() {
		return "", errors.New("missing release")
	}
	return p, nil
}
func (s *Server) static(w http.ResponseWriter, r *http.Request, root string) {
	// Reject traversal before cleaning, including encoded slashes decoded by net/http.
	for _, seg := range strings.Split(r.URL.Path, "/") {
		if seg == ".." || seg == "." || strings.Contains(seg, "\\") {
			fail(w, 400, "invalid_path", "Invalid path")
			return
		}
	}
	rel := strings.TrimPrefix(r.URL.Path, "/")
	if rel == "" {
		rel = "index.html"
	}
	p := filepath.Join(root, filepath.FromSlash(rel))
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		p = filepath.Join(root, "index.html")
	}
	// Releases contain only validated regular files, and are never writable by clients.
	if strings.HasSuffix(p, "index.html") {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "private, max-age=3600")
	}
	if typ := mime.TypeByExtension(filepath.Ext(p)); typ != "" {
		w.Header().Set("Content-Type", typ)
	}
	http.ServeFile(w, r, p)
}
func (s *Server) admin(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Token)) != 1 {
		fail(w, 401, "unauthorized", "Valid deployment token required")
		return
	}
	if r.URL.Path == "/_pmq/admin/apps" && r.Method == http.MethodGet {
		entries, err := os.ReadDir(filepath.Join(s.cfg.DataDir, "apps"))
		if err != nil {
			fail(w, 500, "internal_error", "Could not list applications")
			return
		}
		names := []string{}
		for _, e := range entries {
			if e.IsDir() && ValidName(e.Name()) {
				if _, err := s.current(e.Name()); err == nil {
					names = append(names, e.Name())
				}
			}
		}
		sort.Strings(names)
		reply(w, 200, map[string]any{"apps": names})
		return
	}
	const prefix = "/_pmq/admin/deploy/"
	if !strings.HasPrefix(r.URL.Path, prefix) || r.Method != http.MethodPost {
		fail(w, 404, "not_found", "Endpoint not found")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, prefix)
	if !ValidName(name) {
		fail(w, 400, "invalid_app", "Invalid application name")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxUpload)
	if err := s.deploy(name, r.Body); err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			fail(w, 413, "upload_too_large", "Upload too large")
		} else {
			fail(w, 400, "invalid_upload", "Invalid or incomplete deployment archive")
		}
		return
	}
	reply(w, 201, map[string]string{"name": name, "url": "http://" + name + "." + s.cfg.Domain})
}
func randomID() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func (s *Server) deploy(name string, body io.Reader) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	base := filepath.Join(s.cfg.DataDir, "apps", name)
	releases := filepath.Join(base, "releases")
	if err := os.MkdirAll(releases, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(releases, ".staging-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	gz, err := gzip.NewReader(body)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	total := int64(0)
	count := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		count++
		if count > 10000 {
			return errors.New("too many entries")
		}
		n := h.Name
		if n == "" || strings.Contains(n, "\\") || strings.HasPrefix(n, "/") || path.Clean(n) != n || n == "." || strings.HasPrefix(n, "../") || strings.Contains(n, "/../") {
			return errors.New("unsafe path")
		}
		target := filepath.Join(stage, filepath.FromSlash(n))
		switch h.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(target, 0700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			total += h.Size
			if h.Size < 0 || total > MaxUpload {
				return errors.New("too large")
			}
			if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			f, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				return e
			}
			_, e = io.Copy(f, tr)
			ce := f.Close()
			if e != nil {
				return e
			}
			if ce != nil {
				return ce
			}
		default:
			return errors.New("unsupported entry")
		}
	}
	// Consume gzip trailer and remaining input so truncation and compressed-size limits are enforced.
	if _, err = io.Copy(io.Discard, gz); err != nil {
		return err
	}
	if _, err = io.Copy(io.Discard, body); err != nil {
		return err
	}
	st, err := os.Stat(filepath.Join(stage, "index.html"))
	if err != nil || !st.Mode().IsRegular() {
		return errors.New("missing index")
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	if err = os.Rename(stage, filepath.Join(releases, id)); err != nil {
		return err
	}
	f, err := os.CreateTemp(base, ".current-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.WriteString(id + "\n")
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	return os.Rename(tmp, filepath.Join(base, "current"))
}
func (s *Server) db(app string) (*sql.DB, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if db := s.dbs[app]; db != nil {
		return db, nil
	}
	dir := filepath.Join(s.cfg.DataDir, "state", app)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "data.sqlite")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS documents (collection TEXT NOT NULL, id TEXT NOT NULL, data TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(collection,id))`)
	if err != nil {
		db.Close()
		return nil, err
	}
	s.dbs[app] = db
	return db, nil
}

type document struct {
	ID         string          `json:"id"`
	Collection string          `json:"collection"`
	Data       json.RawMessage `json:"data"`
	CreatedAt  string          `json:"createdAt"`
	UpdatedAt  string          `json:"updatedAt"`
}

func scan(row interface{ Scan(...any) error }) (document, error) {
	var d document
	var data string
	err := row.Scan(&d.ID, &d.Collection, &data, &d.CreatedAt, &d.UpdatedAt)
	d.Data = json.RawMessage(data)
	return d, err
}
func (s *Server) documents(w http.ResponseWriter, r *http.Request, app string) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/_pmq/db/"), "/")
	if len(parts) < 1 || len(parts) > 2 || !ValidName(parts[0]) || (len(parts) == 2 && !idRE.MatchString(parts[1])) {
		fail(w, 400, "invalid_path", "Invalid collection or document ID")
		return
	}
	collection := parts[0]
	db, err := s.db(app)
	if err != nil {
		log.Printf("database open: %v", err)
		fail(w, 500, "internal_error", "Database unavailable")
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		limit, offset := 50, 0
		var e error
		if x := r.URL.Query().Get("limit"); x != "" {
			limit, e = strconv.Atoi(x)
			if e != nil || limit < 1 || limit > 100 {
				fail(w, 400, "invalid_pagination", "Limit must be 1 to 100")
				return
			}
		}
		if x := r.URL.Query().Get("offset"); x != "" {
			offset, e = strconv.Atoi(x)
			if e != nil || offset < 0 || offset > 1000000 {
				fail(w, 400, "invalid_pagination", "Invalid offset")
				return
			}
		}
		rows, e := db.QueryContext(r.Context(), `SELECT id,collection,data,created_at,updated_at FROM documents WHERE collection=? ORDER BY created_at,id LIMIT ? OFFSET ?`, collection, limit, offset)
		if e != nil {
			fail(w, 500, "internal_error", "Database error")
			return
		}
		defer rows.Close()
		docs := []document{}
		for rows.Next() {
			d, e := scan(rows)
			if e != nil {
				fail(w, 500, "internal_error", "Database error")
				return
			}
			docs = append(docs, d)
		}
		if rows.Err() != nil {
			fail(w, 500, "internal_error", "Database error")
			return
		}
		reply(w, 200, map[string]any{"documents": docs, "limit": limit, "offset": offset})
		return
	}
	if len(parts) == 2 && r.Method == http.MethodGet {
		d, e := scan(db.QueryRowContext(r.Context(), `SELECT id,collection,data,created_at,updated_at FROM documents WHERE collection=? AND id=?`, collection, parts[1]))
		if errors.Is(e, sql.ErrNoRows) {
			fail(w, 404, "document_not_found", "Document does not exist")
			return
		}
		if e != nil {
			fail(w, 500, "internal_error", "Database error")
			return
		}
		reply(w, 200, d)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodDelete {
		res, e := db.ExecContext(r.Context(), `DELETE FROM documents WHERE collection=? AND id=?`, collection, parts[1])
		if e != nil {
			fail(w, 500, "internal_error", "Database error")
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fail(w, 404, "document_not_found", "Document does not exist")
			return
		}
		w.WriteHeader(204)
		return
	}
	if (len(parts) == 1 && r.Method == http.MethodPost) || (len(parts) == 2 && r.Method == http.MethodPut) {
		r.Body = http.MaxBytesReader(w, r.Body, MaxJSON)
		b, e := io.ReadAll(r.Body)
		if e != nil {
			fail(w, 413, "body_too_large", "Request body too large")
			return
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(b, &obj) != nil || obj == nil {
			fail(w, 400, "invalid_json", "Expected a JSON object")
			return
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		id := ""
		if len(parts) == 1 {
			id, e = randomID()
			if e == nil {
				_, e = db.ExecContext(r.Context(), `INSERT INTO documents VALUES(?,?,?,?,?)`, collection, id, string(b), now, now)
			}
		} else {
			id = parts[1]
			var result sql.Result
			result, e = db.ExecContext(r.Context(), `UPDATE documents SET data=?,updated_at=? WHERE collection=? AND id=?`, string(b), now, collection, id)
			if e == nil {
				n, _ := result.RowsAffected()
				if n == 0 {
					fail(w, 404, "document_not_found", "Document does not exist")
					return
				}
			}
		}
		if e != nil {
			fail(w, 500, "internal_error", "Database error")
			return
		}
		d, e := scan(db.QueryRowContext(r.Context(), `SELECT id,collection,data,created_at,updated_at FROM documents WHERE collection=? AND id=?`, collection, id))
		if e != nil {
			fail(w, 500, "internal_error", "Database error")
			return
		}
		status := 200
		if len(parts) == 1 {
			status = 201
		}
		reply(w, status, d)
		return
	}
	fail(w, 405, "method_not_allowed", "Method not allowed")
}
