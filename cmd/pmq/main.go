package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/pmq-dev/pmq/internal/pmq"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var version = "dev"

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
		return errors.New("usage: pmq serve|deploy|list|version")
	}
	switch args[0] {
	case "version":
		fmt.Println("pmq", version)
		return nil
	case "serve":
		return serve(args[1:])
	case "deploy":
		return deploy(args[1:])
	case "list":
		return list(args[1:])
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
	token := f.String("token", env("PMQ_DEPLOY_TOKEN", ""), "deployment token (prefer environment)")
	header := f.String("identity-header", env("PMQ_IDENTITY_HEADER", "X-PMQ-User"), "proxy identity header")
	proxies := f.String("trusted-proxies", env("PMQ_TRUSTED_PROXIES", ""), "comma-separated IPs or CIDRs")
	if err := f.Parse(args); err != nil {
		return err
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
	s, err := pmq.New(pmq.Config{Listen: *listen, DataDir: *data, Domain: *domain, Dev: *dev, Token: *token, IdentityHeader: *header, TrustedProxies: ps})
	if err != nil {
		return err
	}
	defer s.Close()
	srv := &http.Server{Addr: *listen, Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "PMQ listening on", ln.Addr())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	err = srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
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
		return errors.New("usage: pmq deploy DIRECTORY --name NAME")
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
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
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
