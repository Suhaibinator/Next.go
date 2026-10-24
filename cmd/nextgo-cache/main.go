// Command nextgo-cache serves the bounded shared cache with memory or durable
// single-owner local-file storage.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Suhaibinator/Next.go/cache/shared"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx, os.Args[1:], os.Getenv("NEXTGO_CACHE_TOKEN"), os.Stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "nextgo-cache:", err)
		os.Exit(1)
	}
}
func execute(ctx context.Context, args []string, token string, out io.Writer) (result error) {
	flags := flag.NewFlagSet("nextgo-cache", flag.ContinueOnError)
	flags.SetOutput(out)
	addr := flags.String("addr", "127.0.0.1:3050", "private listen address; non-loopback requires TLS")
	namespace := flags.String("namespace", "demo", "application/tenant namespace")
	entries := flags.Int("entries", 1024, "maximum retained data keys, including leases")
	budget := flags.Int64("bytes", 64<<20, "maximum accounted ordinary data bytes")
	controlBudget := flags.Int64("control-bytes", shared.DefaultControlBytes, "maximum seed ticket/session/seen-key control bytes")
	dataDir := flags.String("data-dir", "", "durable private cache directory; empty selects memory")
	diskBudget := flags.Int64("disk-bytes", 0, "durable active journal limit; zero uses the backend default")
	concurrency := flags.Int("concurrency", 32, "maximum active operations")
	cert := flags.String("tls-cert", "", "TLS certificate file")
	key := flags.String("tls-key", "", "TLS private key file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *entries <= 0 || *budget <= 0 || *controlBudget <= 0 || *diskBudget < 0 {
		return errors.New("cache entry, data and control limits must be positive and disk-bytes nonnegative")
	}
	if *dataDir == "" && *diskBudget != 0 {
		return errors.New("disk-bytes requires data-dir")
	}
	host, _, err := net.SplitHostPort(*addr)
	if err != nil {
		return errors.New("addr must be host:port")
	}
	ip := net.ParseIP(host)
	loopback := strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()
	if *cert == "" && *key != "" || *cert != "" && *key == "" || !loopback && *cert == "" {
		return errors.New("non-loopback addresses require both tls-cert and tls-key")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var backend shared.Backend
	mode := "memory only"
	if *dataDir != "" {
		file, err := shared.OpenFile(*dataDir, shared.FileOptions{Capacity: *entries, MaxBytes: *budget, MaxControlBytes: *controlBudget, MaxDiskBytes: *diskBudget})
		if err != nil {
			return err
		}
		backend = file
		mode = "durable file storage"
		// Install cleanup immediately: configuration, TLS, status output and
		// listener startup can all fail after ownership has been acquired.
		defer func() {
			if err := file.Close(); err != nil {
				result = errors.Join(result, fmt.Errorf("close durable cache: %w", err))
			}
		}()
	} else {
		backend, err = shared.NewMemoryWithOptions(shared.MemoryOptions{Capacity: *entries, MaxBytes: *budget, MaxControlBytes: *controlBudget})
		if err != nil {
			return err
		}
	}
	handler, err := shared.NewHandler(shared.HandlerOptions{Backend: backend, Namespace: *namespace, Token: token, MaxConcurrent: *concurrency})
	if err != nil {
		return errors.New("configure a valid namespace, limits and NEXTGO_CACHE_TOKEN (32..256 visible ASCII bytes)")
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	if *cert != "" {
		pair, err := tls.LoadX509KeyPair(*cert, *key)
		if err != nil {
			return errors.New("cannot load TLS certificate/key")
		}
		server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	if _, err := fmt.Fprintf(out, "Next.go reference shared cache: %s (namespace %s; %s)\n", listener.Addr(), *namespace, mode); err != nil {
		return fmt.Errorf("write startup status: %w", err)
	}
	return serve(ctx, server, listener)
}

// Every Serve outcome drains/force-closes HTTP before execute closes storage.
// File.Close then serializes with any operation already admitted by a handler.
func serve(ctx context.Context, server *http.Server, listener net.Listener) error {
	done := make(chan error, 1)
	go func() {
		if server.TLSConfig != nil {
			done <- server.ServeTLS(listener, "", "")
		} else {
			done <- server.Serve(listener)
		}
	}()
	var serveErr error
	finished := false
	select {
	case serveErr = <-done:
		finished = true
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	drainErr := server.Shutdown(shutdown)
	var forceErr error
	if drainErr != nil {
		forceErr = server.Close()
	}
	if !finished {
		serveErr = <-done
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, drainErr, forceErr)
}
