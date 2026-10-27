// Package serverless adapts net/http applications to AWS Lambda HTTP API v2
// events (including Function URLs) using the custom runtime protocol.
// Responses default to buffering; Run also supports opt-in response streaming.
// WebSockets and payload v1 are unsupported.
package serverless

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Request is the API Gateway HTTP API payload version 2.0 request envelope.
// RawQueryString preserves repeated query parameters and their original order.
type Request struct {
	Version         string            `json:"version"`
	RawPath         string            `json:"rawPath"`
	RawQueryString  string            `json:"rawQueryString"`
	Cookies         []string          `json:"cookies,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	IsBase64Encoded bool              `json:"isBase64Encoded"`
	RequestContext  RequestContext    `json:"requestContext"`
}

type RequestContext struct {
	DomainName string      `json:"domainName"`
	RequestID  string      `json:"requestId"`
	HTTP       HTTPContext `json:"http"`
}

type HTTPContext struct {
	Method   string `json:"method"`
	Path     string `json:"path"`
	Protocol string `json:"protocol"`
	SourceIP string `json:"sourceIp"`
}

// Response is the explicit HTTP API v2 response envelope. Set-Cookie values
// remain separate in Cookies; other repeated response headers are comma joined.
type Response struct {
	StatusCode      int               `json:"statusCode"`
	Headers         map[string]string `json:"headers,omitempty"`
	Cookies         []string          `json:"cookies,omitempty"`
	Body            string            `json:"body"`
	IsBase64Encoded bool              `json:"isBase64Encoded"`
}

// HTTPRequest reconstructs an incoming server request. Lambda does not expose
// client disconnects; cancellation follows ctx and the invocation deadline.
func (event Request) HTTPRequest(ctx context.Context) (*http.Request, error) {
	if event.Version != "2.0" {
		return nil, fmt.Errorf("unsupported HTTP event version %q; require 2.0", event.Version)
	}
	method := event.RequestContext.HTTP.Method
	if method == "" || !validToken(method) {
		return nil, fmt.Errorf("invalid HTTP method")
	}
	path := event.RawPath
	if path == "" {
		path = event.RequestContext.HTTP.Path
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "?#\r\n") {
		return nil, fmt.Errorf("invalid raw path")
	}
	decoded, err := url.PathUnescape(path)
	if err != nil {
		return nil, fmt.Errorf("invalid raw path: %w", err)
	}
	if strings.ContainsAny(event.RawQueryString, "#\r\n") {
		return nil, fmt.Errorf("invalid raw query")
	}
	body := []byte(event.Body)
	if event.IsBase64Encoded {
		body, err = base64.StdEncoding.DecodeString(event.Body)
		if err != nil {
			return nil, fmt.Errorf("decode request body: %w", err)
		}
	}
	headers := make(http.Header)
	for name, value := range event.Headers {
		if !validToken(name) || strings.ContainsAny(value, "\r\n\x00") {
			return nil, fmt.Errorf("invalid request header")
		}
		headers.Add(name, value)
	}
	if len(event.Cookies) > 0 {
		for _, cookie := range event.Cookies {
			if strings.ContainsAny(cookie, "\r\n\x00") {
				return nil, fmt.Errorf("invalid cookie")
			}
		}
		headers.Set("Cookie", strings.Join(event.Cookies, "; "))
	}
	host := headers.Get("Host")
	if host == "" {
		host = event.RequestContext.DomainName
	}
	if host == "" || strings.ContainsAny(host, "/\\?#@ \t\r\n\x00") {
		return nil, fmt.Errorf("invalid host")
	}
	headers.Del("Host")
	headers.Del("Transfer-Encoding")
	headers.Del("Content-Length")
	scheme := "https"
	if headers.Get("X-Forwarded-Proto") == "http" {
		scheme = "http"
	}
	u := &url.URL{Scheme: scheme, Host: host, Path: decoded, RawPath: path, RawQuery: event.RawQueryString}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(withGatewayRequestID(req.Context(), event.RequestContext.RequestID))
	req.Header = headers
	req.Host = host
	if scheme == "https" {
		req.TLS = &tls.ConnectionState{HandshakeComplete: true}
	}
	req.RequestURI = path
	if event.RawQueryString != "" {
		req.RequestURI += "?" + event.RawQueryString
	}
	req.Proto = event.RequestContext.HTTP.Protocol
	if req.Proto == "" {
		req.Proto = "HTTP/1.1"
	}
	major, minor, ok := http.ParseHTTPVersion(req.Proto)
	if !ok {
		return nil, fmt.Errorf("invalid HTTP protocol")
	}
	req.ProtoMajor, req.ProtoMinor = major, minor
	if ip := event.RequestContext.HTTP.SourceIP; ip != "" {
		req.RemoteAddr = net.JoinHostPort(ip, "0")
	}
	// This is a server request, so callers must not accidentally attempt a replay.
	req.GetBody = nil
	return req, nil
}

// Adapt invokes a handler and buffers its response. A panic discards the partial
// response and becomes a generic HTTP 500; malformed envelopes return an error.
func Adapt(ctx context.Context, handler http.Handler, event Request) (response Response, err error) {
	if handler == nil {
		return response, fmt.Errorf("nil HTTP handler")
	}
	req, err := event.HTTPRequest(ctx)
	if err != nil {
		return response, err
	}
	defer req.Body.Close()
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("nextgo serverless: handler panic (%T)", recovered)
			response = Response{StatusCode: 500, Headers: map[string]string{"content-type": "text/plain; charset=utf-8"}, Body: "Internal Server Error\n"}
			err = nil
		}
	}()
	w := &responseWriter{header: make(http.Header), method: req.Method}
	handler.ServeHTTP(w, req)
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.overflow {
		return response, fmt.Errorf("HTTP response exceeds buffered Lambda payload limit")
	}
	response = Response{StatusCode: w.status, Headers: make(map[string]string)}
	for name, values := range w.sent {
		if strings.EqualFold(name, "Set-Cookie") {
			response.Cookies = append(response.Cookies, values...)
			continue
		}
		if hopHeader(name) || (strings.EqualFold(name, "Content-Length") && req.Method != "HEAD") || strings.EqualFold(name, "Trailer") {
			continue
		}
		connectionHeader := false
		for _, n := range strings.Split(w.sent.Get("Connection"), ",") {
			if strings.EqualFold(name, strings.TrimSpace(n)) {
				connectionHeader = true
			}
		}
		if connectionHeader {
			continue
		}
		response.Headers[strings.ToLower(name)] = strings.Join(values, ",")
	}
	if w.body.Len() > 0 && response.Headers["content-type"] == "" {
		response.Headers["content-type"] = http.DetectContentType(w.body.Bytes())
	}
	body := w.body.Bytes()
	if !utf8.Valid(body) || binaryContent(response.Headers["content-type"]) || response.Headers["content-encoding"] != "" {
		response.IsBase64Encoded = true
		response.Body = base64.StdEncoding.EncodeToString(body)
	} else {
		response.Body = string(body)
	}
	return response, nil
}

const maxResponseBody = 4 * 1024 * 1024 // leaves room for base64 and envelope under Lambda's 6 MB limit
type responseWriter struct {
	header, sent http.Header
	body         bytes.Buffer
	status       int
	method       string
	overflow     bool
}

func (w *responseWriter) Header() http.Header { return w.header }
func (w *responseWriter) WriteHeader(code int) {
	if code < 100 || code > 999 {
		panic("invalid WriteHeader code " + strconv.Itoa(code))
	}
	if w.status != 0 {
		return
	}
	if code >= 100 && code < 200 {
		return
	} // informational responses cannot be represented by the adapter
	w.status = code
	w.sent = w.header.Clone()
}
func (w *responseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if w.status == 204 || w.status == 205 || w.status == 304 {
		return 0, http.ErrBodyNotAllowed
	}
	if w.method == "HEAD" {
		return len(p), nil
	}
	if w.body.Len()+len(p) > maxResponseBody {
		w.overflow = true
		return 0, fmt.Errorf("Lambda buffered response too large")
	}
	return w.body.Write(p)
}
func validToken(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c > 127 || !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	return true
}
func hopHeader(name string) bool {
	switch strings.ToLower(name) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	}
	return false
}
func binaryContent(content string) bool {
	content = strings.ToLower(strings.TrimSpace(strings.Split(content, ";")[0]))
	return content != "" && !strings.HasPrefix(content, "text/") && content != "application/json" && content != "application/javascript" && content != "application/xml" && !strings.HasSuffix(content, "+json") && !strings.HasSuffix(content, "+xml") && content != "image/svg+xml"
}
