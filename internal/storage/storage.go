// Package storage provides app-scoped access to a configured object bucket.
package storage

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-kit/log"
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

type Store struct{ bucket objstore.Bucket }
type App struct {
	bucket objstore.Bucket
	prefix string
}

// New checks S3 connectivity and list permission so invalid credentials fail at startup.
func New(ctx context.Context, c Config) (*Store, error) {
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
		if err == nil {
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
	default:
		return nil, fmt.Errorf("unknown storage provider %q", c.Provider)
	}
	if err != nil {
		return nil, fmt.Errorf("initialize %s storage: %w", c.Provider, err)
	}
	return &Store{bucket: b}, nil
}

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
	if key == "" || len(key) > 160 || strings.ContainsAny(key, "\\\x00") {
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
