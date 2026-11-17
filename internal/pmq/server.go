package pmq

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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

	"github.com/pmq-dev/pmq/internal/storage"
	_ "modernc.org/sqlite"
)

const MaxUpload = 32 << 20
const MaxJSON = 1 << 20

var nameRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var idRE = regexp.MustCompile(`^[a-f0-9]{32}$`)

func ValidName(s string) bool {
	return storage.ValidApp(s)
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
	Storage                                        storage.Config
	AI                                             AIConfig
	AIProvider                                     ChatProvider
	AIRequestTimeout                               time.Duration
}
type Server struct {
	cfg                   Config
	mu                    sync.Mutex
	dbs                   map[string]*sql.DB
	storage               *storage.Store
	metrics               *prometheus.Registry
	transferCancellations *prometheus.CounterVec
	aiProvider            ChatProvider
	aiTimeout             time.Duration
	httpRequests          *prometheus.CounterVec
	httpDuration          *prometheus.HistogramVec
	httpInFlight          *prometheus.GaugeVec
	storageHTTP           *prometheus.CounterVec
	dbOperations          *prometheus.CounterVec
	dbDuration            *prometheus.HistogramVec
	dbRowsReturned        *prometheus.CounterVec
	dbErrors              *prometheus.CounterVec
	aiLimits              AILimits
	aiAdmission           *aiAdmission
}

func New(c Config) (*Server, error) {
	c.AI.Limits = normalizeAILimits(c.AI.Limits)
	if err := validateAILimits(c.AI.Limits, c.AI.Models); err != nil {
		return nil, err
	}
	if (c.AI.Limits.GlobalBudgetUSD > 0 || c.AI.Limits.PerAppBudgetUSD > 0) && len(c.AI.Models) == 0 {
		return nil, errors.New("AI budgets require configured model aliases")
	}
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
	c.Storage.DataDir = c.DataDir
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	registry := prometheus.NewRegistry()
	httpRequests := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "pmq_http_requests_total", Help: "HTTP requests handled by PMQ."}, []string{"route", "method", "status_class"})
	httpDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "pmq_http_request_duration_seconds", Help: "HTTP request duration in seconds.", Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}}, []string{"route", "method"})
	httpInFlight := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "pmq_http_requests_in_flight", Help: "HTTP requests currently being handled."}, []string{"route"})
	storageHTTP := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "pmq_storage_http_operations_total", Help: "Storage API operations by bounded outcome."}, []string{"operation", "outcome"})
	dbOperations := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "pmq_sqlite_document_operations_total", Help: "SQLite document API operations by bounded outcome."}, []string{"operation", "outcome"})
	dbDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "pmq_sqlite_document_operation_duration_seconds", Help: "SQLite document API operation duration in seconds.", Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}}, []string{"operation", "outcome"})
	dbRowsReturned := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "pmq_sqlite_document_rows_returned_total", Help: "Rows returned by SQLite document API operations."}, []string{"operation"})
	dbErrors := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "pmq_sqlite_document_errors_total", Help: "SQLite document operation errors by bounded category."}, []string{"operation", "error"})
	registry.MustRegister(httpRequests, httpDuration, httpInFlight, storageHTTP, dbOperations, dbDuration, dbRowsReturned, dbErrors)
	cancellations := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "pmq_storage_transfer_cancellations_total",
		Help: "Number of storage transfers canceled by the request context.",
	}, []string{"operation"})
	registry.MustRegister(cancellations)
	store, err := storage.NewWithRegistry(ctx, c.Storage, registry)
	if err != nil {
		return nil, err
	}
	aiProvider := c.AIProvider
	if aiProvider == nil {
		aiProvider, err = newAIProvider(c.AI)
		if err != nil {
			_ = store.Close()
			return nil, err
		}
	}
	aiTimeout := c.AIRequestTimeout
	if aiTimeout <= 0 {
		aiTimeout = c.AI.Limits.Timeout
	}
	return &Server{
		cfg: c, dbs: make(map[string]*sql.DB), storage: store, metrics: registry,
		transferCancellations: cancellations, httpRequests: httpRequests, httpDuration: httpDuration,
		httpInFlight: httpInFlight, storageHTTP: storageHTTP, dbOperations: dbOperations,
		dbDuration: dbDuration, dbRowsReturned: dbRowsReturned, dbErrors: dbErrors,
		aiProvider: aiProvider, aiTimeout: aiTimeout, aiLimits: c.AI.Limits, aiAdmission: newAIAdmission(c.AI.Limits),
	}, nil
}
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.storage.Close(); err != nil {
		log.Printf("close storage: %v", err)
	}
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
	route := metricRoute(r.URL.Path)
	method := metricMethod(r.Method)
	s.httpInFlight.WithLabelValues(route).Inc()
	started := time.Now()
	tracked := &responseMetricsWriter{ResponseWriter: w}
	defer func() {
		status := tracked.status
		if status == 0 {
			status = http.StatusOK
		}
		s.httpInFlight.WithLabelValues(route).Dec()
		s.httpRequests.WithLabelValues(route, method, statusClass(status)).Inc()
		s.httpDuration.WithLabelValues(route, method).Observe(time.Since(started).Seconds())
		if strings.HasPrefix(route, "/_pmq/storage") {
			s.storageHTTP.WithLabelValues(storageOperation(route, method), storageOutcome(status, r.Context().Err())).Inc()
		}
	}()
	s.serveHTTP(tracked, r)
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
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
		if r.URL.Path == "/_pmq/admin/metrics" {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Token)) != 1 {
				fail(w, 401, "unauthorized", "Valid deployment token required")
				return
			}
			if r.Method != http.MethodGet {
				fail(w, 405, "method_not_allowed", "Method not allowed")
				return
			}
			promhttp.HandlerFor(s.metrics, promhttp.HandlerOpts{}).ServeHTTP(w, r)
			return
		}
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
		} else if r.URL.Path == "/_pmq/storage" || strings.HasPrefix(r.URL.Path, "/_pmq/storage/") {
			s.objects(w, r, app)
		} else if strings.HasPrefix(r.URL.Path, "/_pmq/ai/") {
			s.ai(w, r, app)
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

type responseMetricsWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseMetricsWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseMetricsWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *responseMetricsWriter) ReadFrom(r io.Reader) (int64, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(r)
	}
	return io.Copy(w.ResponseWriter, r)
}

func (w *responseMetricsWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func metricMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodHead, http.MethodOptions:
		return method
	default:
		return "OTHER"
	}
}

func metricRoute(path string) string {
	switch path {
	case "/_pmq/health":
		return path
	case "/_pmq/admin/metrics":
		return path
	case "/_pmq/storage", "/_pmq/storage/":
		return "/_pmq/storage/list"
	}
	switch {
	case strings.HasPrefix(path, "/_pmq/db/"):
		if strings.Count(strings.Trim(strings.TrimPrefix(path, "/_pmq/db/"), "/"), "/") == 0 {
			return "/_pmq/db/{collection}"
		}
		return "/_pmq/db/{collection}/{id}"
	case strings.HasPrefix(path, "/_pmq/storage/"):
		return "/_pmq/storage/{key}"
	case strings.HasPrefix(path, "/_pmq/admin/deploy/"):
		return "/_pmq/admin/deploy/{name}"
	case strings.HasPrefix(path, "/_pmq/admin/status/"):
		return "/_pmq/admin/status/{name}"
	case strings.HasPrefix(path, "/_pmq/admin/releases/"):
		return "/_pmq/admin/releases/{name}"
	case strings.HasPrefix(path, "/_pmq/admin/rollback/"):
		return "/_pmq/admin/rollback/{name}"
	case strings.HasPrefix(path, "/_pmq/admin/prune/"):
		return "/_pmq/admin/prune/{name}"
	case strings.HasPrefix(path, "/_pmq/admin/"):
		return "/_pmq/admin/other"
	case strings.HasPrefix(path, "/_pmq/"):
		return "/_pmq/other"
	default:
		return "/static"
	}
}

func statusClass(status int) string {
	switch status / 100 {
	case 1:
		return "1xx"
	case 2:
		return "2xx"
	case 3:
		return "3xx"
	case 4:
		return "4xx"
	case 5:
		return "5xx"
	default:
		return "other"
	}
}

func storageOperation(route, method string) string {
	if route == "/_pmq/storage/list" {
		return "list"
	}
	switch method {
	case http.MethodPut:
		return "upload"
	case http.MethodGet:
		return "download"
	case http.MethodHead:
		return "head"
	case http.MethodDelete:
		return "delete"
	default:
		return "other"
	}
}

func storageOutcome(status int, ctxErr error) string {
	if ctxErr != nil {
		return "canceled"
	}
	switch status {
	case 200, 201, 204, 206:
		return "success"
	case 404:
		return "not_found"
	case 401:
		return "unauthorized"
	case 403:
		return "forbidden"
	case 413:
		return "too_large"
	default:
		if status >= 500 {
			return "provider_error"
		}
		if status >= 400 {
			return "rejected"
		}
		return "other"
	}
}

const MaxObject = 32 << 20

func (s *Server) objects(w http.ResponseWriter, r *http.Request, app string) {
	store, err := s.storage.ForApp(app)
	if err != nil {
		fail(w, 500, "internal_error", "Storage unavailable")
		return
	}
	if r.URL.Path == "/_pmq/storage" || r.URL.Path == "/_pmq/storage/" {
		if r.Method != http.MethodGet {
			fail(w, 405, "method_not_allowed", "Method not allowed")
			return
		}
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			limit, err = strconv.Atoi(raw)
			if err != nil || limit < 1 || limit > 500 {
				fail(w, 400, "invalid_pagination", "Limit must be 1 to 500")
				return
			}
		}
		items, next, e := store.List(r.Context(), r.URL.Query().Get("prefix"), r.URL.Query().Get("cursor"), limit)
		if e != nil {
			if errors.Is(e, storage.ErrInvalidKey) {
				fail(w, 400, "invalid_key", "Invalid key, prefix or cursor")
			} else {
				storageError(w, e)
			}
			return
		}
		reply(w, 200, map[string]any{"objects": items, "limit": limit, "nextCursor": next})
		return
	}
	raw := strings.TrimPrefix(r.URL.EscapedPath(), "/_pmq/storage/")
	key, err := storage.DecodeKey(raw)
	if err != nil {
		fail(w, 400, "invalid_key", "Invalid object key")
		return
	}
	switch r.Method {
	case http.MethodPut:
		r.Body = http.MaxBytesReader(w, r.Body, MaxObject)
		ct := r.Header.Get("Content-Type")
		if ct == "" {
			ct = mime.TypeByExtension(filepath.Ext(key))
			if ct == "" {
				ct = "application/octet-stream"
			}
		}
		if _, _, e := mime.ParseMediaType(ct); e != nil {
			fail(w, 400, "invalid_content_type", "Invalid Content-Type")
			return
		}
		body := &limitedReadCloser{Reader: r.Body}
		defer body.Close()
		e := store.Upload(r.Context(), key, body)
		if e != nil {
			if r.Context().Err() != nil {
				s.transferCancellations.WithLabelValues("upload").Inc()
			}
			var max *http.MaxBytesError
			if errors.As(e, &max) {
				fail(w, 413, "object_too_large", "Object exceeds 32 MiB limit")
			} else if errors.Is(e, storage.ErrInvalidKey) {
				fail(w, 400, "invalid_key", "Invalid object key")
			} else {
				fail(w, 500, "storage_error", "Could not store object")
			}
			return
		}
		w.WriteHeader(http.StatusCreated)
	case http.MethodGet, http.MethodHead:
		meta, e := store.Head(r.Context(), key)
		if e != nil {
			if r.Context().Err() != nil {
				s.transferCancellations.WithLabelValues("download").Inc()
			}
			storageError(w, e)
			return
		}
		modified, _ := time.Parse(time.RFC3339, meta.LastModified)
		w.Header().Set("Content-Type", meta.ContentType)
		w.Header().Set("Content-Length", strconv.FormatInt(meta.Size, 10))
		if !modified.IsZero() {
			w.Header().Set("Last-Modified", modified.UTC().Format(http.TimeFormat))
		}
		if meta.ETag != "" {
			w.Header().Set("ETag", meta.ETag)
		}
		if matchesIfMatch(r.Header.Get("If-Match"), meta.ETag) == false && r.Header.Get("If-Match") != "" {
			w.Header().Del("Content-Length")
			fail(w, http.StatusPreconditionFailed, "precondition_failed", "If-Match condition failed")
			return
		}
		if raw := r.Header.Get("If-Unmodified-Since"); raw != "" {
			if date, parseErr := http.ParseTime(raw); parseErr == nil && !modified.IsZero() && modified.Truncate(time.Second).After(date) {
				w.Header().Del("Content-Length")
				fail(w, http.StatusPreconditionFailed, "precondition_failed", "If-Unmodified-Since condition failed")
				return
			}
		}
		if matchesIfNoneMatch(r.Header.Get("If-None-Match"), meta.ETag) || (r.Header.Get("If-None-Match") == "" && notModifiedSince(r.Header.Get("If-Modified-Since"), modified)) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		offset, length, partial := int64(0), meta.Size, false
		if raw := r.Header.Get("Range"); r.Method == http.MethodGet && raw != "" && (r.Header.Get("If-Range") == "" || ifRangeMatches(r.Header.Get("If-Range"), meta.ETag, modified)) {
			var valid bool
			offset, length, valid = parseByteRange(raw, meta.Size)
			if !valid {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", meta.Size))
				w.Header().Del("Content-Length")
				fail(w, http.StatusRequestedRangeNotSatisfiable, "invalid_range", "Requested byte range is not satisfiable")
				return
			}
			partial = true
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, offset+length-1, meta.Size))
			w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
		}
		var body io.ReadCloser
		if r.Method == http.MethodGet {
			if partial {
				body, e = store.GetRange(r.Context(), key, offset, length)
			} else {
				body, e = store.Get(r.Context(), key)
			}
			if e != nil {
				if r.Context().Err() != nil {
					s.transferCancellations.WithLabelValues("download").Inc()
				}
				clearObjectHeaders(w)
				storageError(w, e)
				return
			}
			defer body.Close()
		}
		if r.Method == http.MethodHead {
			if partial {
				w.WriteHeader(http.StatusPartialContent)
			} else {
				w.WriteHeader(http.StatusOK)
			}
			return
		}
		if partial {
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		if _, e = io.Copy(w, body); e != nil && r.Context().Err() != nil {
			s.transferCancellations.WithLabelValues("download").Inc()
		}
	case http.MethodDelete:
		e := store.Delete(r.Context(), key)
		if e != nil {
			storageError(w, e)
			return
		}
		w.WriteHeader(204)
	default:
		w.Header().Set("Allow", "GET, HEAD, PUT, DELETE")
		fail(w, 405, "method_not_allowed", "Method not allowed")
	}
}

type limitedReadCloser struct{ io.Reader }

func (l *limitedReadCloser) Close() error {
	if c, ok := l.Reader.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func parseByteRange(value string, size int64) (offset, length int64, ok bool) {
	if size <= 0 || !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes="), "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	if parts[0] == "" {
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, false
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, suffix, true
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	end := size - 1
	if parts[1] != "" {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end < start {
			return 0, 0, false
		}
		if end >= size {
			end = size - 1
		}
	}
	return start, end - start + 1, true
}

func matchesIfMatch(value, etag string) bool {
	if value == "" {
		return true
	}
	for _, candidate := range strings.Split(value, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || (etag != "" && !strings.HasPrefix(candidate, "W/") && candidate == etag) {
			return true
		}
	}
	return false
}

func matchesIfNoneMatch(value, etag string) bool {
	for _, candidate := range strings.Split(value, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || (etag != "" && strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(etag, "W/")) {
			return true
		}
	}
	return false
}

func notModifiedSince(value string, modified time.Time) bool {
	date, err := http.ParseTime(value)
	return value != "" && err == nil && !modified.IsZero() && !modified.Truncate(time.Second).After(date)
}

func ifRangeMatches(value, etag string, modified time.Time) bool {
	if strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "W/") {
		return etag != "" && !strings.HasPrefix(value, "W/") && value == etag
	}
	date, err := http.ParseTime(value)
	return err == nil && !modified.IsZero() && !modified.Truncate(time.Second).After(date)
}

func storageError(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrNotFound) {
		fail(w, 404, "object_not_found", "Object does not exist")
	} else if errors.Is(err, storage.ErrInvalidKey) {
		fail(w, 400, "invalid_key", "Invalid object key")
	} else {
		fail(w, 500, "storage_error", "Storage operation failed")
	}
}

func clearObjectHeaders(w http.ResponseWriter) {
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Last-Modified", "ETag"} {
		w.Header().Del(name)
	}
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
	if strings.HasPrefix(r.URL.Path, "/_pmq/admin/status/") && r.Method == http.MethodGet {
		name := strings.TrimPrefix(r.URL.Path, "/_pmq/admin/status/")
		if !ValidName(name) {
			fail(w, 400, "invalid_app", "Invalid application name")
			return
		}
		s.mu.Lock()
		id, err := s.releaseID(name)
		s.mu.Unlock()
		if err != nil {
			fail(w, 404, "app_not_found", "Application not found")
			return
		}
		reply(w, 200, map[string]string{"name": name, "activeRelease": id})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_pmq/admin/releases/") && r.Method == http.MethodGet {
		name := strings.TrimPrefix(r.URL.Path, "/_pmq/admin/releases/")
		if !ValidName(name) {
			fail(w, 400, "invalid_app", "Invalid application name")
			return
		}
		s.mu.Lock()
		releases, active, err := s.listReleases(name)
		s.mu.Unlock()
		if err != nil {
			fail(w, 404, "app_not_found", "Application not found")
			return
		}
		reply(w, 200, map[string]any{"name": name, "activeRelease": active, "releases": releases})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_pmq/admin/rollback/") && r.Method == http.MethodPost {
		name := strings.TrimPrefix(r.URL.Path, "/_pmq/admin/rollback/")
		if !ValidName(name) {
			fail(w, 400, "invalid_app", "Invalid application name")
			return
		}
		var in struct {
			Release string `json:"release"`
		}
		if r.Body != nil {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
				fail(w, 400, "invalid_request", "Invalid rollback request")
				return
			}
		}
		s.mu.Lock()
		id, err := s.rollback(name, in.Release)
		s.mu.Unlock()
		if err != nil {
			fail(w, 400, "invalid_release", "Could not activate the requested release")
			return
		}
		reply(w, 200, map[string]string{"name": name, "activeRelease": id})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_pmq/admin/prune/") && r.Method == http.MethodPost {
		name := strings.TrimPrefix(r.URL.Path, "/_pmq/admin/prune/")
		if !ValidName(name) {
			fail(w, 400, "invalid_app", "Invalid application name")
			return
		}
		keep, err := strconv.Atoi(r.URL.Query().Get("keep"))
		if err != nil || keep < 1 || keep > 1000 {
			fail(w, 400, "invalid_retention", "keep must be between 1 and 1000")
			return
		}
		s.mu.Lock()
		removed, err := s.prune(name, keep)
		s.mu.Unlock()
		if err != nil {
			fail(w, 400, "prune_failed", "Could not prune releases")
			return
		}
		reply(w, 200, map[string]any{"name": name, "removed": removed})
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

type releaseInfo struct {
	ID        string    `json:"id"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Server) releaseID(app string) (string, error) {
	b, err := os.ReadFile(filepath.Join(s.cfg.DataDir, "apps", app, "current"))
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if !idRE.MatchString(id) {
		return "", errors.New("invalid active release")
	}
	st, err := os.Stat(filepath.Join(s.cfg.DataDir, "apps", app, "releases", id))
	if err != nil || !st.IsDir() {
		return "", errors.New("missing active release")
	}
	return id, nil
}
func (s *Server) listReleases(app string) ([]releaseInfo, string, error) {
	active, err := s.releaseID(app)
	if err != nil {
		return nil, "", err
	}
	dir := filepath.Join(s.cfg.DataDir, "apps", app, "releases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, "", err
	}
	out := make([]releaseInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || !idRE.MatchString(e.Name()) {
			continue
		}
		st, err := e.Info()
		if err != nil {
			return nil, "", err
		}
		out = append(out, releaseInfo{ID: e.Name(), Active: e.Name() == active, CreatedAt: st.ModTime().UTC()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, active, nil
}
func (s *Server) activate(app, id string) error {
	base := filepath.Join(s.cfg.DataDir, "apps", app)
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
func (s *Server) rollback(app, id string) (string, error) {
	base := filepath.Join(s.cfg.DataDir, "apps", app)
	if id == "" {
		releases, _, err := s.listReleases(app)
		if err != nil {
			return "", err
		}
		for _, r := range releases {
			if !r.Active {
				id = r.ID
				break
			}
		}
	}
	if !idRE.MatchString(id) {
		return "", errors.New("release ID is required and must be valid")
	}
	st, err := os.Stat(filepath.Join(base, "releases", id))
	if err != nil || !st.IsDir() {
		return "", errors.New("release does not exist")
	}
	if err := s.activate(app, id); err != nil {
		return "", err
	}
	return id, nil
}
func (s *Server) prune(app string, keep int) ([]string, error) {
	releases, active, err := s.listReleases(app)
	if err != nil {
		return nil, err
	}
	keepSet := map[string]bool{active: true}
	// Keep one alternative for rollback even when the active release is newest.
	for _, release := range releases {
		if release.ID != active {
			keepSet[release.ID] = true
			break
		}
	}
	for i := 0; i < len(releases) && i < keep; i++ {
		keepSet[releases[i].ID] = true
	}
	var removed []string
	for _, r := range releases {
		if keepSet[r.ID] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.cfg.DataDir, "apps", app, "releases", r.ID)); err != nil {
			return removed, err
		}
		removed = append(removed, r.ID)
	}
	return removed, nil
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

func documentOperation(parts []string, method string) string {
	if len(parts) == 1 {
		if method == http.MethodPost {
			return "create"
		}
		if method == http.MethodGet {
			return "list"
		}
	}
	if len(parts) == 2 {
		switch method {
		case http.MethodGet:
			return "get"
		case http.MethodPut:
			return "update"
		case http.MethodDelete:
			return "delete"
		}
	}
	return "other"
}

func sqliteErrorCategory(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "canceled"
	}
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		switch coded.Code() & 0xff {
		case 5:
			return "busy"
		case 6:
			return "locked"
		case 19:
			return "constraint"
		}
	}
	return "other"
}

func (s *Server) recordDBOperation(operation, outcome string, started time.Time, rows int, err error) {
	s.dbOperations.WithLabelValues(operation, outcome).Inc()
	s.dbDuration.WithLabelValues(operation, outcome).Observe(time.Since(started).Seconds())
	if rows > 0 {
		s.dbRowsReturned.WithLabelValues(operation).Add(float64(rows))
	}
	if err != nil {
		s.dbErrors.WithLabelValues(operation, sqliteErrorCategory(err)).Inc()
	}
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
	operation := documentOperation(parts, r.Method)
	if operation == "other" {
		fail(w, 405, "method_not_allowed", "Method not allowed")
		return
	}
	dbStarted := time.Now()
	db, err := s.db(app)
	if err != nil {
		log.Printf("database open: %v", err)
		s.recordDBOperation(operation, sqliteErrorCategory(err), dbStarted, 0, err)
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
			s.recordDBOperation(operation, sqliteErrorCategory(e), dbStarted, 0, e)
			fail(w, 500, "internal_error", "Database error")
			return
		}
		defer rows.Close()
		docs := []document{}
		for rows.Next() {
			d, e := scan(rows)
			if e != nil {
				s.recordDBOperation(operation, sqliteErrorCategory(e), dbStarted, len(docs), e)
				fail(w, 500, "internal_error", "Database error")
				return
			}
			docs = append(docs, d)
		}
		if rows.Err() != nil {
			s.recordDBOperation(operation, sqliteErrorCategory(rows.Err()), dbStarted, len(docs), rows.Err())
			fail(w, 500, "internal_error", "Database error")
			return
		}
		s.recordDBOperation(operation, "success", dbStarted, len(docs), nil)
		reply(w, 200, map[string]any{"documents": docs, "limit": limit, "offset": offset})
		return
	}
	if len(parts) == 2 && r.Method == http.MethodGet {
		d, e := scan(db.QueryRowContext(r.Context(), `SELECT id,collection,data,created_at,updated_at FROM documents WHERE collection=? AND id=?`, collection, parts[1]))
		if errors.Is(e, sql.ErrNoRows) {
			s.recordDBOperation(operation, "not_found", dbStarted, 0, nil)
			fail(w, 404, "document_not_found", "Document does not exist")
			return
		}
		if e != nil {
			s.recordDBOperation(operation, sqliteErrorCategory(e), dbStarted, 0, e)
			fail(w, 500, "internal_error", "Database error")
			return
		}
		s.recordDBOperation(operation, "success", dbStarted, 1, nil)
		reply(w, 200, d)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodDelete {
		res, e := db.ExecContext(r.Context(), `DELETE FROM documents WHERE collection=? AND id=?`, collection, parts[1])
		if e != nil {
			s.recordDBOperation(operation, sqliteErrorCategory(e), dbStarted, 0, e)
			fail(w, 500, "internal_error", "Database error")
			return
		}
		n, e := res.RowsAffected()
		if e != nil {
			s.recordDBOperation(operation, sqliteErrorCategory(e), dbStarted, 0, e)
			fail(w, 500, "internal_error", "Database error")
			return
		}
		if n == 0 {
			s.recordDBOperation(operation, "not_found", dbStarted, 0, nil)
			fail(w, 404, "document_not_found", "Document does not exist")
			return
		}
		s.recordDBOperation(operation, "success", dbStarted, 0, nil)
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
		started := time.Now()
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
				n, rowsErr := result.RowsAffected()
				if rowsErr != nil {
					s.recordDBOperation(operation, sqliteErrorCategory(rowsErr), started, 0, rowsErr)
					fail(w, 500, "internal_error", "Database error")
					return
				}
				if n == 0 {
					s.recordDBOperation(operation, "not_found", started, 0, nil)
					fail(w, 404, "document_not_found", "Document does not exist")
					return
				}
			}
		}
		if e != nil {
			s.recordDBOperation(operation, sqliteErrorCategory(e), started, 0, e)
			fail(w, 500, "internal_error", "Database error")
			return
		}
		d, e := scan(db.QueryRowContext(r.Context(), `SELECT id,collection,data,created_at,updated_at FROM documents WHERE collection=? AND id=?`, collection, id))
		if e != nil {
			s.recordDBOperation(operation, sqliteErrorCategory(e), started, 0, e)
			fail(w, 500, "internal_error", "Database error")
			return
		}
		s.recordDBOperation(operation, "success", started, 1, nil)
		status := 200
		if len(parts) == 1 {
			status = 201
		}
		reply(w, status, d)
		return
	}
	fail(w, 405, "method_not_allowed", "Method not allowed")
}
