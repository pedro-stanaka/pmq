package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDevelopmentTLSConfiguration(t *testing.T) {
	t.Setenv("PMQ_DEPLOY_TOKEN", "test")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing key", []string{"--dev", "--tls-cert", "cert.pem"}},
		{"missing certificate", []string{"--dev", "--tls-key", "key.pem"}},
		{"production", []string{"--tls-cert", "cert.pem", "--tls-key", "key.pem"}},
		{"non-loopback", []string{"--dev", "--listen", "0.0.0.0:0", "--tls-cert", "cert.pem", "--tls-key", "key.pem"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := serve(tc.args); err == nil {
				t.Fatal("expected configuration rejection")
			}
		})
	}
}

func TestTLSListener(t *testing.T) {
	if os.Getenv("PMQ_TEST_TLS_CHILD") == "1" {
		if err := serve([]string{"--dev", "--listen", os.Getenv("PMQ_TEST_ADDR"), "--data-dir", os.Getenv("PMQ_TEST_DATA"), "--domain", "pmq.dev", "--tls-cert", os.Getenv("PMQ_TEST_CERT"), "--tls-key", os.Getenv("PMQ_TEST_KEY")}); err != nil {
			t.Fatal(err)
		}
		return
	}
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"pmq.dev"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	encodedKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey}), 0600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	child := exec.Command(os.Args[0], "-test.run=^TestTLSListener$")
	child.Env = append(os.Environ(), "PMQ_TEST_TLS_CHILD=1", "PMQ_TEST_ADDR="+addr, "PMQ_TEST_DATA="+filepath.Join(dir, "data"), "PMQ_TEST_CERT="+certPath, "PMQ_TEST_KEY="+keyPath, "PMQ_DEPLOY_TOKEN=test")
	var output bytes.Buffer
	child.Stdout = &output
	child.Stderr = &output
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = child.Process.Signal(os.Interrupt)
		if err := child.Wait(); err != nil {
			t.Errorf("child exited: %v: %s", err, output.String())
		}
	}()
	pool := x509.NewCertPool()
	pool.AddCert(mustParseCert(t, der))
	client := http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "pmq.dev", MinVersion: tls.VersionTLS12}}}
	var response *http.Response
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		response, err = client.Get("https://" + addr + "/_pmq/health")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("HTTPS request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal(response.Status)
	}
}

func TestAIServerWriteTimeoutTracksRequestTimeout(t *testing.T) {
	if got, want := aiServerWriteTimeout(0), 60*time.Second; got != want {
		t.Fatalf("default server write timeout = %s, want %s", got, want)
	}
	if got, want := aiServerWriteTimeout(10*time.Minute), 10*time.Minute+15*time.Second; got != want {
		t.Fatalf("configured server write timeout = %s, want %s", got, want)
	}
}

func mustParseCert(t *testing.T, der []byte) *x509.Certificate {
	t.Helper()
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
