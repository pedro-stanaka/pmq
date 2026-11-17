// Package storage provides app-scoped access to a configured object bucket.
package storage

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/thanos-io/objstore"
	"github.com/thanos-io/objstore/providers/filesystem"
	"github.com/thanos-io/objstore/providers/s3"
)

var ErrNotFound = errors.New("object not found")
var ErrInvalidKey = errors.New("invalid object key")

type Config struct {
	Provider, DataDir                  string
	Endpoint, Region, Bucket           string
	AccessKey, SecretKey, SessionToken string
	Insecure, PathStyle                bool
}

type Store struct {
	bucket   objstore.Bucket
	registry *prometheus.Registry
}
type App struct {
	bucket objstore.Bucket
	prefix string
}

// New checks S3 connectivity and list permission so invalid credentials fail at startup.
func New(ctx context.Context, c Config) (*Store, error) {
	return NewWithRegistry(ctx, c, prometheus.NewRegistry())
}

func NewWithRegistry(ctx context.Context, c Config, registry *prometheus.Registry) (*Store, error) {
	var b objstore.Bucket
	var err error
	switch c.Provider {
	case "", "filesystem":
		if c.DataDir == "" {
			return nil, errors.New("storage data directory is required")
		}
		dir := filepath.Join(c.DataDir, "objects")
		if err = os.MkdirAll(dir, 0700); err == nil {
			b, err = filesystem.NewBucket(dir)
		}
	case "s3":
		if c.Endpoint == "" || c.Region == "" || c.Bucket == "" || c.AccessKey == "" || c.SecretKey == "" {
			return nil, errors.New("S3 requires endpoint, region, bucket, access key and secret key")
		}
		cfg := s3.DefaultConfig
		cfg.Endpoint, cfg.Region, cfg.Bucket = c.Endpoint, c.Region, c.Bucket
		cfg.AccessKey, cfg.SecretKey, cfg.SessionToken = c.AccessKey, c.SecretKey, c.SessionToken
		cfg.Insecure = c.Insecure
		if c.PathStyle {
			cfg.BucketLookupType = s3.PathLookup
		}
		b, err = s3.NewBucketWithConfig(log.NewNopLogger(), cfg, "pmq", nil)
	default:
		return nil, fmt.Errorf("unknown storage provider %q", c.Provider)
	}
	if err != nil {
		return nil, fmt.Errorf("initialize %s storage: %w", c.Provider, err)
	}
	if registry == nil {
		registry = prometheus.NewRegistry()
	}
	b = objstore.WrapWithMetrics(b, registry, "pmq")
	if c.Provider == "s3" {
		stop := errors.New("storage access confirmed")
		err = b.Iter(ctx, "apps/", func(string) error { return stop })
		if errors.Is(err, stop) {
			err = nil
		}
		if err != nil {
			_ = b.Close()
			return nil, fmt.Errorf("S3 bucket access check failed: %w", err)
		}
	}
	return &Store{bucket: b, registry: registry}, nil
}

func (s *Store) Registry() *prometheus.Registry { return s.registry }

func (s *Store) Close() error { return s.bucket.Close() }

var appName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ValidApp matches the hostname router's app naming rules.
func ValidApp(app string) bool {
	if len(app) == 0 || len(app) > 63 || !appName.MatchString(app) {
		return false
	}
	switch app {
	case "www", "api", "admin", "_pmq", "pmq":
		return false
	}
	return true
}

// ForApp accepts only a validated server-derived application name.
func (s *Store) ForApp(app string) (*App, error) {
	if !ValidApp(app) {
		return nil, errors.New("invalid application name")
	}
	return &App{bucket: s.bucket, prefix: "apps/" + app + "/"}, nil
}

func (a *App) key(key string) (string, error) {
	if key == "" || len(key) > 160 || !utf8.ValidString(key) || strings.ContainsAny(key, "\\\x00") {
		return "", ErrInvalidKey
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return "", ErrInvalidKey
		}
		for _, r := range part {
			if r < 32 || r == 127 {
				return "", ErrInvalidKey
			}
		}
	}
	// A flat encoding avoids filesystem directory and temporary-file collisions.
	return a.prefix + base64.RawURLEncoding.EncodeToString([]byte(key)), nil
}
func (a *App) Upload(ctx context.Context, key string, r io.Reader) error {
	name, err := a.key(key)
	if err != nil {
		return err
	}
	return a.bucket.Upload(ctx, name, r)
}
func (a *App) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	name, err := a.key(key)
	if err != nil {
		return nil, err
	}
	r, err := a.bucket.Get(ctx, name)
	if err != nil && a.bucket.IsObjNotFoundErr(err) {
		return nil, ErrNotFound
	}
	return r, err
}

func (a *App) GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	name, err := a.key(key)
	if err != nil {
		return nil, err
	}
	r, err := a.bucket.GetRange(ctx, name, offset, length)
	if err != nil && a.bucket.IsObjNotFoundErr(err) {
		return nil, ErrNotFound
	}
	return r, err
}
func (a *App) Delete(ctx context.Context, key string) error {
	name, err := a.key(key)
	if err != nil {
		return err
	}
	err = a.bucket.Delete(ctx, name)
	if err != nil && a.bucket.IsObjNotFoundErr(err) {
		return ErrNotFound
	}
	return err
}

type Object struct {
	Key          string `json:"key"`
	Size         int64  `json:"size"`
	LastModified string `json:"lastModified"`
	ContentType  string `json:"contentType,omitempty"`
	ETag         string `json:"etag,omitempty"`
}

func (a *App) Head(ctx context.Context, key string) (Object, error) {
	name, err := a.key(key)
	if err != nil {
		return Object{}, err
	}
	attrs, err := a.bucket.Attributes(ctx, name)
	if err != nil && a.bucket.IsObjNotFoundErr(err) {
		return Object{}, ErrNotFound
	}
	if err != nil {
		return Object{}, err
	}
	object := Object{Key: key, Size: attrs.Size, LastModified: attrs.LastModified.UTC().Format("2006-01-02T15:04:05Z07:00"), ContentType: contentType(key)}
	if attrs.Version != nil && attrs.Version.Type == objstore.ETag && attrs.Version.Value != "" {
		object.ETag = `"` + strings.ReplaceAll(attrs.Version.Value, `"`, `\"`) + `"`
	}
	return object, nil
}

func contentType(key string) string {
	if typ := mime.TypeByExtension(filepath.Ext(key)); typ != "" {
		return typ
	}
	return "application/octet-stream"
}

func (a *App) List(ctx context.Context, prefix, cursor string, limit int) ([]Object, string, error) {
	if len(prefix) > 160 || !utf8.ValidString(prefix) || strings.ContainsAny(prefix, "\\\x00") || strings.Contains(prefix, "//") || strings.HasPrefix(prefix, "/") || strings.Contains(prefix, "../") || prefix == ".." || strings.Contains(prefix, "/./") {
		return nil, "", ErrInvalidKey
	}
	for _, r := range prefix {
		if r < 32 || r == 127 {
			return nil, "", ErrInvalidKey
		}
	}
	for _, part := range strings.Split(strings.TrimSuffix(prefix, "/"), "/") {
		if part == "." || part == ".." {
			return nil, "", ErrInvalidKey
		}
	}
	if limit < 1 {
		return nil, "", ErrInvalidKey
	}
	start := ""
	if cursor != "" {
		// A valid key is at most 160 bytes, so its raw URL-safe base64 cursor
		// cannot exceed 214 bytes. Bound decoding before allocating from input.
		if len(cursor) > 214 {
			return nil, "", ErrInvalidKey
		}
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return nil, "", ErrInvalidKey
		}
		start = string(b)
		if _, err = a.key(start); err != nil {
			return nil, "", err
		}
	}
	keys := make([]string, 0, limit+1)
	err := a.bucket.IterWithAttributes(ctx, a.prefix, func(attrs objstore.IterObjectAttributes) error {
		name := strings.TrimPrefix(attrs.Name, a.prefix)
		raw, err := base64.RawURLEncoding.DecodeString(name)
		if err != nil {
			return nil
		}
		key := string(raw)
		if key <= start || !strings.HasPrefix(key, prefix) {
			return nil
		}
		if _, err := a.key(key); err != nil {
			return nil
		}
		idx := sort.SearchStrings(keys, key)
		if idx < len(keys) && keys[idx] == key {
			return nil
		}
		if idx >= limit+1 {
			return nil
		}
		keys = append(keys, "")
		copy(keys[idx+1:], keys[idx:])
		keys[idx] = key
		if len(keys) > limit+1 {
			keys = keys[:limit+1]
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	more := len(keys) > limit
	if more {
		keys = keys[:limit]
	}
	items := make([]Object, 0, len(keys))
	for _, key := range keys {
		item, e := a.Head(ctx, key)
		if e != nil {
			return nil, "", e
		}
		items = append(items, item)
	}
	next := ""
	if more {
		next = base64.RawURLEncoding.EncodeToString([]byte(keys[len(keys)-1]))
	}
	return items, next, nil
}

// DecodeKey decodes an escaped URL path key and rejects malformed escapes.
func DecodeKey(raw string) (string, error) {
	key, err := url.PathUnescape(raw)
	if err != nil {
		return "", ErrInvalidKey
	}
	return key, nil
}
