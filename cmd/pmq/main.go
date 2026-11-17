package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pmq-dev/pmq/internal/pmq"
	"github.com/pmq-dev/pmq/internal/storage"
)

var (
	// Overridden at build time by the Makefile, Dockerfile, and GoReleaser
	// (-X main.version=... etc.) so released binaries report their release.
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const help = `pmq - Poor Man's Quick: build it, drop it, run it.

Usage: pmq <command> [flags]

Commands:
  serve     Run the PMQ server (see 'pmq serve --help' for flags)
  deploy    Upload a built static app directory or .zip archive
  list      List deployed apps
  status    Show an app's active release
  releases  List an app's releases
  rollback  Activate a previous release
  prune     Remove old releases beyond the newest N
  version   Print version, commit, and build date
  help      Show this help

Documentation: https://github.com/pedro-stanaka/pmq
`

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "pmq:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("no command given; run 'pmq help' for usage")
	}
	switch args[0] {
	case "version":
		fmt.Printf("pmq %s (commit %s, built %s)\n", version, commit, date)
		return nil
	case "help", "--help", "-h":
		fmt.Print(help)
		return nil
	case "serve":
		return serve(args[1:])
	case "deploy":
		return deploy(args[1:])
	case "list":
		return list(args[1:])
	case "status":
		return appCommand(args[1:], "status", "GET", "")
	case "releases":
		return appCommand(args[1:], "releases", "GET", "")
	case "rollback":
		return rollback(args[1:])
	case "prune":
		return prune(args[1:])
	default:
		return errors.New("unknown command")
	}
}
func serve(args []string) error {
	f := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := f.String("listen", env("PMQ_LISTEN", "127.0.0.1:8080"), "listen address")
	data := f.String("data-dir", env("PMQ_DATA_DIR", "./data"), "data directory")
	domain := f.String("domain", env("PMQ_DOMAIN", "apps.localhost"), "application domain")
	dev := f.Bool("dev", env("PMQ_DEV", "") == "true", "explicit loopback-only development mode")
	cert := f.String("tls-cert", "", "development TLS certificate file")
	key := f.String("tls-key", "", "development TLS private key file")
	token := f.String("token", env("PMQ_DEPLOY_TOKEN", ""), "deployment token (prefer environment)")
	header := f.String("identity-header", env("PMQ_IDENTITY_HEADER", "X-PMQ-User"), "proxy identity header")
	proxies := f.String("trusted-proxies", env("PMQ_TRUSTED_PROXIES", ""), "comma-separated IPs or CIDRs")
	if err := f.Parse(args); err != nil {
		return err
	}
	if (*cert == "") != (*key == "") {
		return errors.New("tls-cert and tls-key must be provided together")
	}
	if *cert != "" && !*dev {
		return errors.New("built-in TLS is available only in loopback development mode")
	}
	var ps []netip.Prefix
	for _, v := range strings.Split(*proxies, ",") {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		p, e := netip.ParsePrefix(v)
		if e != nil {
			ip, err := netip.ParseAddr(v)
			if err != nil {
				return fmt.Errorf("invalid trusted proxy %q", v)
			}
			p = netip.PrefixFrom(ip, ip.BitLen())
		}
		ps = append(ps, p)
	}
	insecure, err := storageBool("PMQ_S3_INSECURE")
	if err != nil {
		return err
	}
	pathStyle, err := storageBool("PMQ_S3_PATH_STYLE")
	if err != nil {
		return err
	}
	storageConfig := storage.Config{Provider: env("PMQ_STORAGE_PROVIDER", "filesystem"), Endpoint: os.Getenv("PMQ_S3_ENDPOINT"), Region: os.Getenv("PMQ_S3_REGION"), Bucket: os.Getenv("PMQ_S3_BUCKET"), AccessKey: os.Getenv("PMQ_S3_ACCESS_KEY"), SecretKey: os.Getenv("PMQ_S3_SECRET_KEY"), SessionToken: os.Getenv("PMQ_S3_SESSION_TOKEN"), Insecure: insecure, PathStyle: pathStyle}
	parseModels := func(raw, envName string) (map[string]pmq.ModelBinding, error) {
		models := make(map[string]pmq.ModelBinding)
		for _, entry := range strings.Split(raw, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			alias, value, found := strings.Cut(entry, "=")
			if !found {
				return nil, fmt.Errorf("%s entries must be alias=provider|model-id[|stream]", envName)
			}
			parts := strings.Split(value, "|")
			if len(parts) < 2 || len(parts) > 3 || strings.TrimSpace(alias) == "" || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
				return nil, fmt.Errorf("%s entries must be alias=provider|model-id[|stream]", envName)
			}
			alias, provider, model := strings.TrimSpace(alias), strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			streaming := false
			if len(parts) == 3 {
				if strings.TrimSpace(parts[2]) != "stream" {
					return nil, fmt.Errorf("%s entry %q has unknown capability %q", envName, alias, parts[2])
				}
				streaming = true
			}
			if _, exists := models[alias]; exists {
				return nil, fmt.Errorf("duplicate model alias %q in %s", alias, envName)
			}
			models[alias] = pmq.ModelBinding{Provider: provider, ModelID: model, Streaming: streaming}
		}
		return models, nil
	}
	aiModels, err := parseModels(os.Getenv("PMQ_AI_MODELS"), "PMQ_AI_MODELS")
	if err != nil {
		return err
	}
	aiLimits, err := aiLimitsFromEnv()
	if err != nil {
		return err
	}
	aiConfig := pmq.AIConfig{Models: aiModels, DefaultModel: os.Getenv("PMQ_AI_DEFAULT_MODEL"), OpenAIKey: os.Getenv("OPENAI_API_KEY"), OpenRouterKey: os.Getenv("OPENROUTER_API_KEY"), AWSRegion: env("AWS_REGION", os.Getenv("AWS_DEFAULT_REGION")), OpenAIBaseURL: os.Getenv("PMQ_OPENAI_BASE_URL"), OpenRouterBaseURL: os.Getenv("PMQ_OPENROUTER_BASE_URL"), BedrockBaseURL: os.Getenv("PMQ_BEDROCK_BASE_URL"), Limits: aiLimits}
	s, err := pmq.New(pmq.Config{Storage: storageConfig, AI: aiConfig, AITraceEndpoint: os.Getenv("PMQ_OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"), Listen: *listen, DataDir: *data, Domain: *domain, Dev: *dev, Token: *token, IdentityHeader: *header, TrustedProxies: ps})
	if err != nil {
		return err
	}
	defer s.Close()
	srv := &http.Server{Addr: *listen, Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: aiServerWriteTimeout(aiLimits.Timeout), IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "PMQ listening on", ln.Addr())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	if *cert != "" {
		err = srv.ServeTLS(ln, *cert, *key)
	} else {
		err = srv.Serve(ln)
	}
	stop()
	<-shutdownDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func storageBool(name string) (bool, error) {
	value := os.Getenv(name)
	if value == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return b, nil
}

func aiLimitsFromEnv() (pmq.AILimits, error) {
	integer := func(name string) (int, error) {
		value := os.Getenv(name)
		if value == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%s must be a non-negative integer", name)
		}
		return n, nil
	}
	int64Value := func(name string) (int64, error) {
		value := os.Getenv(name)
		if value == "" {
			return 0, nil
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%s must be a non-negative integer", name)
		}
		return n, nil
	}
	amount := func(name string) (float64, error) {
		value := os.Getenv(name)
		if value == "" {
			return 0, nil
		}
		n, err := strconv.ParseFloat(value, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%s must be a non-negative amount", name)
		}
		return n, nil
	}
	var l pmq.AILimits
	var err error
	if l.GlobalRPM, err = integer("PMQ_AI_GLOBAL_RPM"); err != nil {
		return l, err
	}
	if l.PerAppRPM, err = integer("PMQ_AI_PER_APP_RPM"); err != nil {
		return l, err
	}
	if l.GlobalConcurrency, err = integer("PMQ_AI_GLOBAL_CONCURRENCY"); err != nil {
		return l, err
	}
	if l.PerAppConcurrency, err = integer("PMQ_AI_PER_APP_CONCURRENCY"); err != nil {
		return l, err
	}
	if l.MaxOutputTokens, err = integer("PMQ_AI_MAX_OUTPUT_TOKENS"); err != nil {
		return l, err
	}
	if l.DefaultOutputTokens, err = integer("PMQ_AI_DEFAULT_OUTPUT_TOKENS"); err != nil {
		return l, err
	}
	if l.MaxRequestBytes, err = int64Value("PMQ_AI_MAX_REQUEST_BYTES"); err != nil {
		return l, err
	}
	if l.MaxResponseBytes, err = int64Value("PMQ_AI_MAX_RESPONSE_BYTES"); err != nil {
		return l, err
	}
	if l.GlobalBudgetUSD, err = amount("PMQ_AI_GLOBAL_BUDGET_USD"); err != nil {
		return l, err
	}
	if l.PerAppBudgetUSD, err = amount("PMQ_AI_PER_APP_BUDGET_USD"); err != nil {
		return l, err
	}
	if value := os.Getenv("PMQ_AI_TIMEOUT"); value != "" {
		l.Timeout, err = time.ParseDuration(value)
		if err != nil || l.Timeout <= 0 {
			return l, errors.New("PMQ_AI_TIMEOUT must be a positive duration such as 45s")
		}
	}
	if value := os.Getenv("PMQ_AI_PRICING"); value != "" {
		if err := json.Unmarshal([]byte(value), &l.Pricing); err != nil {
			return l, fmt.Errorf("PMQ_AI_PRICING must be a JSON object: %w", err)
		}
	}
	return l, nil
}

func aiServerWriteTimeout(aiTimeout time.Duration) time.Duration {
	if aiTimeout <= 0 {
		aiTimeout = 45 * time.Second
	}
	// Leave time to serialize an error/final accounting event after provider timeout.
	return aiTimeout + 15*time.Second
}

func clientFlags(f *flag.FlagSet) (*string, *string) {
	return f.String("server", env("PMQ_SERVER", "http://127.0.0.1:8080"), "server URL"), f.String("token", env("PMQ_DEPLOY_TOKEN", ""), "deployment token")
}
func request(method, server, endpoint, token string, body io.Reader) (*http.Response, error) {
	if token == "" {
		return nil, errors.New("deployment token required")
	}
	u, err := url.Parse(server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, errors.New("server must be an HTTP(S) origin")
	}
	req, err := http.NewRequest(method, server+endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return (&http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
}
func check(resp *http.Response) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("server returned %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}
func deploy(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: pmq deploy DIRECTORY|APP.zip --name NAME")
	}
	dir := args[0]
	f := flag.NewFlagSet("deploy", flag.ContinueOnError)
	name := f.String("name", "", "application name")
	server, token := clientFlags(f)
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if !pmq.ValidName(*name) {
		return errors.New("invalid application name")
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	st, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if st.Mode().IsRegular() {
		// A .zip archive is uploaded as-is; the server unpacks and validates it
		// with the same rules the CLI applies when packing a directory.
		if !strings.EqualFold(filepath.Ext(root), ".zip") {
			return errors.New("deployment path must be a directory or a .zip archive")
		}
		if st.Size() > pmq.MaxUpload {
			return errors.New("deployment too large")
		}
		file, e := os.Open(root)
		if e != nil {
			return e
		}
		defer file.Close()
		resp, e := request("POST", *server, "/_pmq/admin/deploy/"+*name, *token, file)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		if e = check(resp); e != nil {
			return e
		}
		b, e := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if e != nil {
			return e
		}
		fmt.Println(string(b))
		return nil
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errors.New("deployment path must be a directory, not a symlink")
	}
	tmp, err := os.CreateTemp("", "pmq-upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	gz := gzip.NewWriter(tmp)
	tw := tar.NewWriter(gz)
	var total int64
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if p == root {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return fmt.Errorf("unsupported file: %s", p)
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		if info.IsDir() {
			return nil
		}
		total += info.Size()
		if total > pmq.MaxUpload {
			return errors.New("deployment too large")
		}
		h := &tar.Header{Name: filepath.ToSlash(rel), Mode: 0600, Size: info.Size(), Typeflag: tar.TypeReg}
		if e = tw.WriteHeader(h); e != nil {
			return e
		}
		file, e := os.Open(p)
		if e != nil {
			return e
		}
		_, e = io.Copy(tw, file)
		ce := file.Close()
		if e != nil {
			return e
		}
		return ce
	})
	if err != nil {
		return err
	}
	if err = tw.Close(); err != nil {
		return err
	}
	if err = gz.Close(); err != nil {
		return err
	}
	if st, err = tmp.Stat(); err != nil || st.Size() > pmq.MaxUpload {
		return errors.New("compressed deployment too large")
	}
	if _, err = tmp.Seek(0, 0); err != nil {
		return err
	}
	resp, err := request("POST", *server, "/_pmq/admin/deploy/"+*name, *token, tmp)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err = check(resp); err != nil {
		return err
	}
	var result struct {
		URL string `json:"url"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	serverURL, _ := url.Parse(*server)
	appURL, err := url.Parse(result.URL)
	if err != nil {
		return err
	}
	appURL.Scheme = serverURL.Scheme
	if port := serverURL.Port(); port != "" {
		appURL.Host = net.JoinHostPort(appURL.Hostname(), port)
	}
	fmt.Println(appURL.String())
	return nil
}
func list(args []string) error {
	f := flag.NewFlagSet("list", flag.ContinueOnError)
	server, token := clientFlags(f)
	if err := f.Parse(args); err != nil {
		return err
	}
	resp, err := request("GET", *server, "/_pmq/admin/apps", *token, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err = check(resp); err != nil {
		return err
	}
	var v struct {
		Apps []string `json:"apps"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return err
	}
	for _, a := range v.Apps {
		fmt.Println(a)
	}
	return nil
}
func appCommand(args []string, command, method, suffix string) error {
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	server, token := clientFlags(f)
	if len(args) == 0 || !pmq.ValidName(args[0]) {
		return fmt.Errorf("usage: pmq %s APP", command)
	}
	app := args[0]
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("usage: pmq %s APP", command)
	}
	resp, err := request(method, *server, "/_pmq/admin/"+command+"/"+app+suffix, *token, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err = check(resp); err != nil {
		return err
	}
	var body json.RawMessage
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return err
	}
	var out any
	if err = json.Unmarshal(body, &out); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}
func rollback(args []string) error {
	f := flag.NewFlagSet("rollback", flag.ContinueOnError)
	server, token := clientFlags(f)
	release := f.String("release", "", "release ID (defaults to previous release)")
	if len(args) == 0 || !pmq.ValidName(args[0]) {
		return errors.New("usage: pmq rollback APP [--release ID]")
	}
	app := args[0]
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("usage: pmq rollback APP [--release ID]")
	}
	body, _ := json.Marshal(map[string]string{"release": *release})
	resp, err := request("POST", *server, "/_pmq/admin/rollback/"+app, *token, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err = check(resp); err != nil {
		return err
	}
	var out any
	if err = json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err == nil {
		fmt.Println(string(encoded))
	}
	return err
}
func prune(args []string) error {
	f := flag.NewFlagSet("prune", flag.ContinueOnError)
	server, token := clientFlags(f)
	keep := f.Int("keep", 5, "number of newest releases to retain (active release is always retained)")
	if len(args) == 0 || !pmq.ValidName(args[0]) {
		return errors.New("usage: pmq prune APP --keep N")
	}
	app := args[0]
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("usage: pmq prune APP --keep N")
	}
	if *keep < 1 || *keep > 1000 {
		return errors.New("keep must be between 1 and 1000")
	}
	resp, err := request("POST", *server, fmt.Sprintf("/_pmq/admin/prune/%s?keep=%d", app, *keep), *token, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err = check(resp); err != nil {
		return err
	}
	var out any
	if err = json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err == nil {
		fmt.Println(string(encoded))
	}
	return err
}
