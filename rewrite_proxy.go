package nextgo

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Suhaibinator/Next.go/telemetry"
)

// ProxyOptions bounds external rewrites. Zero selects 16 concurrent requests,
// 2 MiB buffered request bodies, 8 MiB streamed response bodies and 30 seconds
// including body ingestion. Limits apply across managed locale mounts. Default
// transport ignores environment proxies; custom transports must honor context.
type ProxyOptions struct {
	MaxConcurrent                     int
	MaxRequestBytes, MaxResponseBytes int64
	Timeout                           time.Duration
	// ForwardCredentials permits Cookie/Authorization to configured origins and
	// Set-Cookie responses from those origins.
	// ForwardClientInfo sends fresh X-Forwarded values derived from this request,
	// never inbound forwarded headers. Origin/Referer are sent only with credentials.
	ForwardCredentials, ForwardClientInfo bool
	Transport                             http.RoundTripper `json:"-"`
}
type rewriteProxy struct {
	options   ProxyOptions
	slots     chan struct{}
	transport http.RoundTripper
	owned     *http.Transport
}

var errProxyResponseLimit = errors.New("nextgo: upstream response exceeds byte limit")

func newRewriteProxy(options ProxyOptions) (*rewriteProxy, error) {
	if options.MaxConcurrent == 0 {
		options.MaxConcurrent = 16
	}
	if options.MaxRequestBytes == 0 {
		options.MaxRequestBytes = 2 << 20
	}
	if options.MaxResponseBytes == 0 {
		options.MaxResponseBytes = 8 << 20
	}
	if options.Timeout == 0 {
		options.Timeout = 30 * time.Second
	}
	if options.MaxConcurrent < 1 || options.MaxConcurrent > 1024 || options.MaxRequestBytes < 1 || options.MaxRequestBytes > 64<<20 || options.MaxResponseBytes < 1 || options.MaxResponseBytes > 64<<20 || options.Timeout < time.Millisecond || options.Timeout > 5*time.Minute {
		return nil, errors.New("nextgo: invalid external rewrite proxy budgets")
	}
	p := &rewriteProxy{options: options, slots: make(chan struct{}, options.MaxConcurrent), transport: options.Transport}
	if p.transport == nil {
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			base = &http.Transport{}
		}
		p.owned = base.Clone()
		p.owned.Proxy = nil
		p.owned.DisableCompression = true
		p.owned.ResponseHeaderTimeout = options.Timeout
		p.owned.MaxConnsPerHost = options.MaxConcurrent
		p.owned.MaxIdleConns = options.MaxConcurrent
		p.owned.MaxIdleConnsPerHost = options.MaxConcurrent
		p.transport = p.owned
	}
	return p, nil
}
func (p *rewriteProxy) closeIdle() {
	if p != nil && p.owned != nil {
		p.owned.CloseIdleConnections()
	}
}
func stripProxyHeaders(header http.Header) {
	for name := range header {
		key := strings.ToLower(strings.TrimPrefix(name, http.TrailerPrefix))
		if key == "traceparent" || key == "tracestate" || key == "baggage" || key == "forwarded" || key == "x-forwarded" || key == "x-real-ip" || strings.HasPrefix(key, "x-forwarded-") || strings.HasPrefix(key, "x-nextgo-") {
			delete(header, name)
		}
	}
}

type cappedProxyBody struct {
	io.ReadCloser
	remaining int64
	sanitize  func()
}

func (b *cappedProxyBody) Read(data []byte) (n int, err error) {
	defer func() {
		if err != nil && b.sanitize != nil {
			b.sanitize()
		}
	}()
	if len(data) == 0 {
		return 0, nil
	}
	if b.remaining == 0 {
		var extra [1]byte
		n, err := b.ReadCloser.Read(extra[:])
		if n > 0 {
			return 0, errProxyResponseLimit
		}
		return 0, err
	}
	if int64(len(data)) > b.remaining {
		data = data[:b.remaining]
	}
	n, err = b.ReadCloser.Read(data)
	b.remaining -= int64(n)
	return n, err
}
func (a *App) serveProxy(w http.ResponseWriter, r *http.Request, target *url.URL, source string) {
	p := a.proxy
	if target.Host == r.Host && target.RequestURI() == requestURI(r) {
		http.Error(w, "Proxy loop detected", 508)
		return
	}
	if _, err := url.ParseQuery(target.RawQuery); err != nil {
		http.Error(w, "Invalid proxy query", 400)
		return
	}
	if r.Method == http.MethodConnect || r.Header.Get("Upgrade") != "" {
		http.Error(w, "Proxy protocol is not supported", http.StatusNotImplemented)
		return
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "Proxy is busy", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), p.options.Timeout)
	defer cancel()
	controller := http.NewResponseController(w)
	if r.ContentLength > p.options.MaxRequestBytes {
		_ = controller.SetReadDeadline(time.Now())
		http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	var body []byte
	if r.Body != nil && r.Body != http.NoBody {
		deadline, _ := ctx.Deadline()
		_ = controller.SetReadDeadline(deadline)
		var err error
		body, err = io.ReadAll(io.LimitReader(r.Body, p.options.MaxRequestBytes+1))
		if int64(len(body)) > p.options.MaxRequestBytes {
			_ = controller.SetReadDeadline(time.Now())
			http.Error(w, "Request body too large", 413)
			return
		}
		if err != nil || ctx.Err() != nil {
			_ = controller.SetReadDeadline(time.Now())
			status := 400
			if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
				status = 408
			}
			http.Error(w, "Unable to read proxy request", status)
			return
		}
		_ = controller.SetReadDeadline(time.Time{})
	}
	clone := r.Clone(ctx)
	clone.Body = http.NoBody
	if len(body) > 0 {
		clone.Body = io.NopCloser(bytes.NewReader(body))
	}
	clone.ContentLength = int64(len(body))
	clone.GetBody = nil
	clone.Trailer = nil
	clone.TransferEncoding = nil
	clone.Close = false
	stripProxyHeaders(w.Header())
	w.Header().Del("Cache-Control")
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.Out.URL = target
			request.Out.Host = target.Host
			request.Out.Trailer = nil
			stripProxyHeaders(request.Out.Header)
			if !p.options.ForwardCredentials {
				for _, key := range []string{"Cookie", "Authorization", "Origin", "Referer"} {
					request.Out.Header.Del(key)
				}
			}
			if p.options.ForwardClientInfo {
				request.SetXForwarded()
			}
		},
		Transport:     telemetry.Transport{Base: p.transport},
		FlushInterval: -1,
		ErrorLog:      log.New(io.Discard, "", 0),
		ErrorHandler: func(out http.ResponseWriter, request *http.Request, err error) {
			// Never expose backend addresses, response bodies, headers or URL errors.
			status := 502
			category := "transport"
			if errors.Is(err, errProxyResponseLimit) {
				category = "response_limit"
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				status = 504
				category = "timeout"
			}
			a.config.Logger.Printf("nextgo proxy route=%s failure=%s (%T)", source, category, err)
			out.Header().Set("Cache-Control", "no-store")
			http.Error(out, "Upstream request failed", status)
		},
		ModifyResponse: func(response *http.Response) error {
			if response.StatusCode == 101 || response.Body != http.NoBody && response.ContentLength > p.options.MaxResponseBytes {
				return errProxyResponseLimit
			}
			stripProxyHeaders(response.Header)
			if !p.options.ForwardCredentials {
				response.Header.Del("Set-Cookie")
			}
			sanitize := func() {
				stripProxyHeaders(response.Trailer)
				if !p.options.ForwardCredentials {
					response.Trailer.Del("Set-Cookie")
				}
			}
			sanitize()
			if ruleSensitive(r.Context()) {
				response.Header.Set("Cache-Control", "private, no-store")
			}
			response.Body = &cappedProxyBody{ReadCloser: response.Body, remaining: p.options.MaxResponseBytes, sanitize: sanitize}
			return nil
		},
	}
	defer stripProxyHeaders(w.Header())
	telemetry.Handle(w, clone, telemetry.Info{Name: "nextgo.proxy"}, proxy.ServeHTTP)
}
