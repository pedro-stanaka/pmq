package storage_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pmq-dev/pmq/internal/pmq"
	"github.com/pmq-dev/pmq/internal/storage"
	dto "github.com/prometheus/client_model/go"
)

func TestStorageContract(t *testing.T) {
	configs := map[string]storage.Config{"filesystem": {DataDir: t.TempDir()}}
	if endpoint := os.Getenv("PMQ_TEST_S3_ENDPOINT"); endpoint != "" {
		configs["s3"] = storage.Config{Provider: "s3", Endpoint: endpoint, Region: os.Getenv("PMQ_TEST_S3_REGION"), Bucket: os.Getenv("PMQ_TEST_S3_BUCKET"), AccessKey: os.Getenv("PMQ_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("PMQ_TEST_S3_SECRET_KEY"), Insecure: true, PathStyle: true}
	}
	for name, cfg := range configs {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			s, err := storage.New(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			app1, err := pmq.HostApp("first.apps.example.com", "apps.example.com")
			if err != nil {
				t.Fatal(err)
			}
			app2, err := pmq.HostApp("second.apps.example.com", "apps.example.com")
			if err != nil {
				t.Fatal(err)
			}
			a, _ := s.ForApp(app1)
			b, _ := s.ForApp(app2)
			key := "test/" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
			defer a.Delete(context.Background(), key)
			defer b.Delete(context.Background(), key)
			if err := a.Upload(ctx, key, strings.NewReader("one")); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Head(ctx, key); err != nil {
				t.Fatal(err)
			}
			rangeBody, err := a.GetRange(ctx, key, 1, 2)
			if err != nil {
				t.Fatal(err)
			}
			rangeData, err := io.ReadAll(rangeBody)
			if closeErr := rangeBody.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if err != nil || string(rangeData) != "ne" {
				t.Fatalf("range get: %q %v", rangeData, err)
			}
			if _, _, err := a.List(ctx, "test/", "", 10); err != nil {
				t.Fatal(err)
			}
			if err := a.Upload(ctx, key, &failingReader{data: []byte("corrupt"), err: errors.New("simulated source failure")}); err == nil {
				t.Fatal("expected failed overwrite")
			}
			preserved, err := a.Get(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			preservedData, err := io.ReadAll(preserved)
			preserved.Close()
			if err != nil || string(preservedData) != "one" {
				t.Fatalf("failed overwrite changed existing object: %q %v", preservedData, err)
			}
			if _, err := b.Get(ctx, key); !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("cross-app read: %v", err)
			}
			if err := b.Upload(ctx, key, strings.NewReader("two")); err != nil {
				t.Fatal(err)
			}
			r, err := a.Get(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(r)
			r.Close()
			if err != nil || string(data) != "one" {
				t.Fatalf("get: %q %v", data, err)
			}
			if err := b.Delete(ctx, key); err != nil {
				t.Fatal(err)
			}
			r, err = a.Get(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			r.Close()
			for _, bad := range []string{"", "../second/key", "/x", "x/../y", "x//y", "x/./y", `x\y`, "x\x00y", strings.Repeat("a", 161)} {
				if err := a.Upload(ctx, bad, strings.NewReader("x")); !errors.Is(err, storage.ErrInvalidKey) {
					t.Errorf("upload %q: %v", bad, err)
				}
				if err := a.Delete(ctx, bad); !errors.Is(err, storage.ErrInvalidKey) {
					t.Errorf("delete %q: %v", bad, err)
				}
			}
			for _, bad := range []string{"../second", "first/../../second", "admin", ""} {
				if _, err := s.ForApp(bad); err == nil {
					t.Errorf("accepted app %q", bad)
				}
			}
			if err := a.Delete(ctx, key); err != nil {
				t.Fatal(err)
			}
			assertBucketMetrics(t, s.Registry())
			canceled, stop := context.WithCancel(ctx)
			stop()
			if err := a.Upload(canceled, key, strings.NewReader("x")); !errors.Is(err, context.Canceled) {
				t.Errorf("canceled upload: %v", err)
			}
			if _, err := a.GetRange(canceled, key, 0, 1); !errors.Is(err, context.Canceled) {
				t.Errorf("canceled range get: %v", err)
			}
		})
	}
}

func assertBucketMetrics(t *testing.T, registry interface {
	Gather() ([]*dto.MetricFamily, error)
}) {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]*dto.MetricFamily, len(families))
	for _, family := range families {
		byName[family.GetName()] = family
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() != "bucket" && label.GetName() != "operation" && label.GetName() != "le" {
					t.Errorf("unexpected high-cardinality metric label %q on %s", label.GetName(), family.GetName())
				}
				if label.GetName() == "bucket" && label.GetValue() != "pmq" {
					t.Errorf("bucket label leaked provider/app name: %q", label.GetValue())
				}
			}
		}
	}
	for _, operation := range []string{"upload", "get", "get_range", "iter", "attributes", "delete"} {
		if sampleValue(byName["objstore_bucket_operations_total"], operation) <= 0 {
			t.Errorf("no %s operation recorded", operation)
		}
	}
	if sampleValue(byName["objstore_bucket_operation_failures_total"], "get") <= 0 {
		t.Error("expected a failed get to be recorded")
	}
	if sampleValue(byName["objstore_bucket_operation_duration_seconds"], "upload") <= 0 {
		t.Error("upload duration was not recorded")
	}
	if sampleValue(byName["objstore_bucket_operation_transferred_bytes"], "upload") <= 0 {
		t.Error("upload bytes were not recorded")
	}
	if sampleValue(byName["objstore_bucket_operation_transferred_bytes"], "get_range") <= 0 {
		t.Error("range bytes were not recorded")
	}
}

type failingReader struct {
	data []byte
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func sampleValue(family *dto.MetricFamily, operation string) float64 {
	if family == nil {
		return 0
	}
	for _, metric := range family.GetMetric() {
		for _, label := range metric.GetLabel() {
			if label.GetName() == "operation" && label.GetValue() == operation {
				switch {
				case metric.GetCounter() != nil:
					return metric.GetCounter().GetValue()
				case metric.GetHistogram() != nil:
					return float64(metric.GetHistogram().GetSampleCount())
				}
			}
		}
	}
	return 0
}

func TestConfiguration(t *testing.T) {
	for _, cfg := range []storage.Config{{Provider: "bad"}, {Provider: "s3"}, {Provider: "filesystem"}} {
		if _, err := storage.New(context.Background(), cfg); err == nil {
			t.Errorf("accepted invalid config %+v", cfg)
		}
	}
}

func TestS3RejectsBadCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>Access denied</Message></Error>`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := storage.New(ctx, storage.Config{Provider: "s3", Endpoint: strings.TrimPrefix(server.URL, "http://"), Region: "us-east-1", Bucket: "test-bucket", AccessKey: "bad", SecretKey: "bad", Insecure: true, PathStyle: true})
	if err == nil || !strings.Contains(err.Error(), "S3 bucket access check failed") {
		t.Fatalf("expected access failure, got %v", err)
	}
}
