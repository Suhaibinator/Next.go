package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/cache/shared"
)

func TestInvalidConfiguration(t *testing.T) {
	for _, args := range [][]string{{"-addr", ":3050"}, {"-addr", "0.0.0.0:3050"}, {"-addr", "[::]:3050"}, {"-addr", "bad"}, {"-entries", "0"}, {"-bytes", "0"}, {"-namespace", "bad/name"}, {"-tls-key", "private"}, {"extra"}, {"-concurrency", "2048"}} {
		if err := execute(context.Background(), args, strings.Repeat("s", 32), io.Discard); err == nil {
			t.Fatal(args)
		}
	}
	if err := execute(context.Background(), nil, "", io.Discard); err == nil {
		t.Fatal("accepted missing token")
	}
}
func TestCanceledStartupDoesNotListen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := execute(ctx, []string{"-addr", "127.0.0.1:0"}, strings.Repeat("s", 32), io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type startupWriter struct{ address chan string }

func (w startupWriter) Write(p []byte) (int, error) {
	var address string
	if _, err := fmt.Sscanf(string(p), "Next.go reference shared cache: %s", &address); err == nil {
		select {
		case w.address <- address:
		default:
		}
	}
	return len(p), nil
}
func TestServiceServesAndShutsDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	address, done := make(chan string, 1), make(chan error, 1)
	token := strings.Repeat("s", 32)
	go func() {
		done <- execute(ctx, []string{"-addr", "127.0.0.1:0", "-namespace", "app"}, token, startupWriter{address})
	}()
	var endpoint string
	select {
	case addr := <-address:
		endpoint = "http://" + addr
	case err := <-done:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("startup timeout")
	}
	b, err := shared.NewHTTP(endpoint, token, &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	v, err := b.Begin(context.Background(), shared.ID{Namespace: "app", Schema: "v1", Key: "hello"}, shared.Policy{FreshFor: time.Second, ExpireAfter: time.Minute}, time.Second)
	if err != nil || v.Lease == "" {
		t.Fatal(v, err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("shutdown timeout")
	}
}

func TestServiceTLSCertificateAndMinimumVersion(t *testing.T) {
	// Reuse the standard library's disposable loopback certificate. No private
	// deployment key or external certificate generator is needed by this test.
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	pair := fixture.TLS.Certificates[0]
	fixture.Close()
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	address, done := make(chan string, 1), make(chan error, 1)
	token := strings.Repeat("t", 32)
	go func() {
		done <- execute(ctx, []string{"-addr", "127.0.0.1:0", "-namespace", "app", "-tls-cert", certPath, "-tls-key", keyPath}, token, startupWriter{address})
	}()
	var endpoint string
	select {
	case addr := <-address:
		endpoint = "https://" + addr
	case err := <-done:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("TLS startup timeout")
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	b, err := shared.NewHTTP(endpoint, token, client)
	if err != nil {
		t.Fatal(err)
	}
	v, err := b.Begin(context.Background(), shared.ID{Namespace: "app", Schema: "v1", Key: "tls"}, shared.Policy{FreshFor: time.Second, ExpireAfter: time.Minute}, time.Second)
	if err != nil || v.Lease == "" {
		t.Fatal(v, err)
	}
	legacy := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12}}
	defer legacy.CloseIdleConnections()
	old := &http.Client{Transport: legacy, Timeout: time.Second}
	if response, err := old.Get(endpoint); err == nil {
		response.Body.Close()
		t.Fatal("service accepted TLS 1.2")
	}
	untrusted := &http.Client{Timeout: time.Second}
	if response, err := untrusted.Get(endpoint); err == nil {
		response.Body.Close()
		t.Fatal("untrusted certificate accepted")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("TLS shutdown timeout")
	}
}
