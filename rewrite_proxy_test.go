package nextgo

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type proxyEcho struct {
	Method, URI, Host, Body string
	Headers                 http.Header
}

func upstreamEcho(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Nextgo-Protocol", "3")
		w.Header().Set("X-Nextgo-Configuration", strings.Repeat("a", 64))
		w.Header().Add("Set-Cookie", "upstream=yes; HttpOnly")
		_ = json.NewEncoder(w).Encode(proxyEcho{r.Method, r.URL.RequestURI(), r.Host, string(data), r.Header})
	}))
}
func TestExternalRewritePinsAuthorityAndStripsPrivateAndFrameworkHeaders(t *testing.T) {
	upstream := upstreamEcho(t)
	defer upstream.Close()
	app := testApp(t, Config{BasePath: "/base", Rewrites: []RewriteRule{{Source: "/proxy/*tail", Destination: upstream.URL + "/backend/*tail"}}})
	r := httptest.NewRequest("POST", "http://frontend.test/base/proxy/a%20b?query=x%26y", strings.NewReader("request body"))
	for _, name := range []string{"Cookie", "Authorization", "Origin", "Referer", "Forwarded", "X-Forwarded-Host", "X-Forwarded-Port", "X-Nextgo-Navigate", "X-Nextgo-Configuration"} {
		r.Header.Set(name, "private-probe")
	}
	r.Header.Set("X-Ordinary", "retained")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	var echo proxyEcho
	if err := json.Unmarshal(w.Body.Bytes(), &echo); err != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	if echo.Method != "POST" || echo.URI != "/backend/a%20b?query=x%26y" || "http://"+echo.Host != upstream.URL || echo.Body != "request body" || echo.Headers.Get("X-Ordinary") != "retained" {
		t.Fatal(echo)
	}
	for name, values := range echo.Headers {
		if strings.Contains(strings.Join(values, "|"), "private-probe") {
			t.Fatal("private request header forwarded", name)
		}
	}
	if w.Header().Get("X-Nextgo-Protocol") != "" || w.Header().Get("X-Nextgo-Configuration") != "" || len(w.Header().Values("Set-Cookie")) != 0 {
		t.Fatal("upstream response ownership", w.Header())
	}
}
func TestExternalRewriteExplicitCredentialsAndFreshForwardedValues(t *testing.T) {
	upstream := upstreamEcho(t)
	defer upstream.Close()
	app := testApp(t, Config{Proxy: ProxyOptions{ForwardCredentials: true, ForwardClientInfo: true}, Rewrites: []RewriteRule{{Source: "/proxy", Destination: upstream.URL}}})
	r := httptest.NewRequest("GET", "https://frontend.test/proxy", nil)
	r.RemoteAddr = "192.0.2.5:3210"
	r.Header.Set("Authorization", "Bearer configured")
	r.Header.Set("Cookie", "session=configured")
	r.Header.Set("Origin", "https://frontend.test")
	r.Header.Set("X-Forwarded-For", "spoofed")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	var echo proxyEcho
	if err := json.Unmarshal(w.Body.Bytes(), &echo); err != nil {
		t.Fatal(err, w.Body.String())
	}
	if len(w.Header().Values("Set-Cookie")) != 1 || echo.Headers.Get("Authorization") != "Bearer configured" || echo.Headers.Get("Cookie") != "session=configured" || echo.Headers.Get("X-Forwarded-For") != "192.0.2.5" || echo.Headers.Get("X-Forwarded-Host") != "frontend.test" || echo.Headers.Get("X-Forwarded-Proto") != "https" {
		t.Fatal(echo.Headers)
	}
}
func TestProxyTimeoutConcurrencyAndAdmissionLimits(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		_, _ = io.WriteString(w, "done")
	}))
	defer upstream.Close()
	defer close(release)
	app := testApp(t, Config{Proxy: ProxyOptions{MaxConcurrent: 1, Timeout: 80 * time.Millisecond, MaxRequestBytes: 8}, Rewrites: []RewriteRule{{Source: "/proxy", Destination: upstream.URL}}})
	completed := make(chan *httptest.ResponseRecorder, 1)
	go func() { completed <- request(app, "/proxy", false) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream did not start")
	}
	if got := request(app, "/proxy", false); got.Code != 503 || got.Header().Get("Retry-After") == "" {
		t.Fatal(got.Code, got.Header())
	}
	select {
	case got := <-completed:
		if got.Code != 504 || strings.Contains(got.Body.String(), upstream.URL) {
			t.Fatal(got.Code, got.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("upstream deadline did not stop transport")
	}
	r := httptest.NewRequest("POST", "/proxy", strings.NewReader("too many bytes"))
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(app.proxy.slots) != 0 {
		t.Fatal("proxy admission not released")
	}
}
func TestProxyResponseLimitsAndStreaming(t *testing.T) {
	release := make(chan struct{})
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		if r.URL.Query().Has("large") {
			w.Header().Set("Content-Length", "100")
			_, _ = io.WriteString(w, strings.Repeat("x", 100))
			return
		}
		_, _ = io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
		_, _ = io.WriteString(w, "last\n")
	}))
	defer upstream.Close()
	defer close(release)
	app := testApp(t, Config{Proxy: ProxyOptions{MaxResponseBytes: 16}, Rewrites: []RewriteRule{{Source: "/proxy", Destination: upstream.URL}}})
	server := httptest.NewServer(app)
	defer server.Close()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(server.URL + "/proxy?large=1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 502 || strings.Contains(string(body), "xxx") {
		t.Fatal(response.StatusCode, string(body))
	}
	response, err = client.Get(server.URL + "/proxy")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	first := make([]byte, 6)
	if _, err := io.ReadFull(response.Body, first); err != nil || string(first) != "first\n" {
		t.Fatal(string(first), err)
	}
}
func TestProxyResponseOverflowAbortsACommittedStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, strings.Repeat("x", 100))
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()
	app := testApp(t, Config{Proxy: ProxyOptions{MaxResponseBytes: 16}, Rewrites: []RewriteRule{{Source: "/proxy", Destination: upstream.URL}}})
	server := httptest.NewServer(app)
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/proxy")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err == nil || len(data) > 16 {
		t.Fatal("committed overflow masqueraded as a complete response", len(data), err)
	}
}
func TestProxyDoesNotFollowRedirectsAndSupportsExplicitTLS(t *testing.T) {
	var touched atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { touched.Add(1) }))
	defer destination.Close()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(307)
	}))
	defer upstream.Close()
	app := testApp(t, Config{Proxy: ProxyOptions{Transport: upstream.Client().Transport}, Rewrites: []RewriteRule{{Source: "/proxy", Destination: upstream.URL}}})
	w := request(app, "/proxy", false)
	if w.Code != 307 || w.Header().Get("Location") != destination.URL || touched.Load() != 0 {
		t.Fatal(w.Code, w.Header(), touched.Load())
	}
}
func TestManagedLocalesShareProxyAdmission(t *testing.T) {
	app := testApp(t, Config{Locales: &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", PrefixDefault: true}, Proxy: ProxyOptions{MaxConcurrent: 1}, Rewrites: []RewriteRule{{Source: "/proxy", Destination: "http://upstream.test"}}})
	for _, scope := range app.locales.mounts {
		if scope.app.proxy != app.proxy {
			t.Fatal("locale duplicated application proxy budget")
		}
	}
	if _, err := app.StaticExportConfig(); err == nil {
		t.Fatal("localized static export discarded proxy routing")
	}
	_ = app.Close(context.Background())
}

func TestProxyFiltersLateProtocolAndCookieTrailers(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Trailer", "X-Nextgo-Protocol")
		w.Header().Add("Trailer", "Set-Cookie")
		w.Header().Add("Trailer", "X-Ordinary-Trailer")
		_, _ = io.WriteString(w, "body")
		w.(http.Flusher).Flush()
		w.Header().Set("X-Nextgo-Protocol", "spoofed")
		w.Header().Set("Set-Cookie", "session=spoofed")
		w.Header().Set("X-Ordinary-Trailer", "retained")
	}))
	defer upstream.Close()
	app := testApp(t, Config{Rewrites: []RewriteRule{{Source: "/proxy", Destination: upstream.URL}}})
	server := httptest.NewServer(app)
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/proxy")
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.Trailer.Get("X-Nextgo-Protocol") != "" || response.Trailer.Get("Set-Cookie") != "" || response.Trailer.Get("X-Ordinary-Trailer") != "retained" {
		t.Fatal(response.Trailer)
	}
}

func TestProxySlowClientBodyHasAReadDeadlineBeforeUpstreamContact(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	app := testApp(t, Config{Proxy: ProxyOptions{Timeout: 30 * time.Millisecond, MaxRequestBytes: 16}, Rewrites: []RewriteRule{{Source: "/proxy", Destination: upstream.URL}}})
	server := httptest.NewServer(app)
	defer server.Close()
	connection, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_, err = io.WriteString(connection, "POST /proxy HTTP/1.1\r\nHost: frontend.test\r\nContent-Length: 8\r\nConnection: close\r\n\r\na")
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: "POST"})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 408 || calls.Load() != 0 {
		t.Fatal(response.StatusCode, calls.Load())
	}
}

func TestProxyHEADRetainsRepresentationLengthWithoutApplyingBodyBudget(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	app := testApp(t, Config{Proxy: ProxyOptions{MaxResponseBytes: 16}, Rewrites: []RewriteRule{{Source: "/proxy", Destination: upstream.URL}}})
	r := httptest.NewRequest("HEAD", "/proxy", nil)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Content-Length") != "1000000" || w.Body.Len() != 0 {
		t.Fatal("HEAD representation metadata was rejected as response body", w.Code, w.Header(), w.Body.String())
	}
}
