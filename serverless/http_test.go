package serverless

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/httpx"
)

func event() Request {
	return Request{Version: "2.0", RawPath: "/posts/a%20b", RawQueryString: "tag=a&tag=b&plus=%2B", Headers: map[string]string{"host": "site.example", "x-test": "value"}, Cookies: []string{"session=abc", "theme=dark"}, RequestContext: RequestContext{DomainName: "fallback.example", HTTP: HTTPContext{Method: "POST", Protocol: "HTTP/2.0", SourceIP: "2001:db8::1"}}}
}

func TestHTTPRequest(t *testing.T) {
	e := event()
	e.Body = base64.StdEncoding.EncodeToString([]byte{0, 255, 5})
	e.IsBase64Encoded = true
	key := struct{}{}
	ctx := context.WithValue(context.Background(), key, "context value")
	r, err := e.HTTPRequest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	if !bytes.Equal(body, []byte{0, 255, 5}) || r.ContentLength != 3 {
		t.Fatalf("body/length: %v %d", body, r.ContentLength)
	}
	if r.URL.Path != "/posts/a b" || r.URL.RawQuery != e.RawQueryString || r.RequestURI != e.RawPath+"?"+e.RawQueryString {
		t.Fatalf("URL: %s requestURI %s", r.URL, r.RequestURI)
	}
	if got := r.URL.Query()["tag"]; len(got) != 2 || got[1] != "b" {
		t.Fatalf("query: %v", got)
	}
	if r.Host != "site.example" || r.RemoteAddr != "[2001:db8::1]:0" || r.ProtoMajor != 2 || r.TLS == nil || r.Context().Value(key) != "context value" {
		t.Fatalf("request: %#v", r)
	}
	if len(r.Cookies()) != 2 || r.Cookies()[0].Name != "session" {
		t.Fatalf("cookies: %v", r.Cookies())
	}
	if r.Header.Get("X-Test") != "value" || r.Header.Get("Host") != "" || r.GetBody != nil {
		t.Fatalf("headers/body: %#v", r)
	}
}

func TestInvalidRequest(t *testing.T) {
	tests := map[string]func(*Request){
		"version":      func(e *Request) { e.Version = "1.0" },
		"method":       func(e *Request) { e.RequestContext.HTTP.Method = "GET\r\n" },
		"path":         func(e *Request) { e.RawPath = "//attacker.test" },
		"escaped path": func(e *Request) { e.RawPath = "/%xx" },
		"query":        func(e *Request) { e.RawQueryString = "a=1#fragment" },
		"host":         func(e *Request) { e.Headers["host"] = "good.test\r\nBad: yes" },
		"header":       func(e *Request) { e.Headers["Bad: Header"] = "x" },
		"cookie":       func(e *Request) { e.Cookies = []string{"a=1\r\nBad: yes"} },
		"body":         func(e *Request) { e.Body = "!!!"; e.IsBase64Encoded = true },
		"protocol":     func(e *Request) { e.RequestContext.HTTP.Protocol = "bogus" },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			e := event()
			change(&e)
			if _, err := e.HTTPRequest(context.Background()); err == nil {
				t.Fatal("accepted malformed event")
			}
		})
	}
}

func TestAdaptResponse(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("Set-Cookie", "one=1; Secure; HttpOnly")
		w.Header().Add("Set-Cookie", "two=2; SameSite=Lax")
		w.Header().Add("Vary", "Accept")
		w.Header().Add("Vary", "Origin")
		w.Header().Set("Connection", "X-Hop")
		w.Header().Set("X-Hop", "secret")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.WriteHeader(103)
		w.WriteHeader(201)
		w.Header().Set("X-Late", "ignored")
		w.WriteHeader(202)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	r, err := Adapt(context.Background(), h, event())
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != 201 || r.Body != `{"ok":true}` || r.IsBase64Encoded || len(r.Cookies) != 2 || r.Headers["vary"] != "Accept,Origin" {
		t.Fatalf("response: %#v", r)
	}
	for _, key := range []string{"connection", "x-hop", "transfer-encoding", "x-late", "set-cookie"} {
		if _, ok := r.Headers[key]; ok {
			t.Fatalf("leaked header %s", key)
		}
	}
	data, _ := json.Marshal(r)
	if !json.Valid(data) {
		t.Fatal("invalid response JSON")
	}
}

func TestAdaptBodies(t *testing.T) {
	for _, test := range []struct {
		name, method, content string
		status                int
		body                  []byte
		wantBase64            bool
		wantBody              []byte
	}{
		{"binary", "GET", "application/wasm", 200, []byte{0, 97, 115, 109}, true, []byte{0, 97, 115, 109}},
		{"invalid utf8", "GET", "text/plain", 200, []byte{255}, true, []byte{255}},
		{"utf8", "GET", "text/plain", 200, []byte("hello 世界"), false, []byte("hello 世界")},
		{"head", "HEAD", "text/plain", 200, []byte("not sent"), false, nil},
		{"204", "GET", "", 204, []byte("not sent"), false, nil},
		{"205", "GET", "", 205, []byte("not sent"), false, nil},
		{"304", "GET", "", 304, []byte("not sent"), false, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := event()
			e.RequestContext.HTTP.Method = test.method
			r, err := Adapt(context.Background(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if test.content != "" {
					w.Header().Set("Content-Type", test.content)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write(test.body)
			}), e)
			if err != nil {
				t.Fatal(err)
			}
			if r.StatusCode != test.status || r.IsBase64Encoded != test.wantBase64 {
				t.Fatalf("response %#v", r)
			}
			got := []byte(r.Body)
			if r.IsBase64Encoded {
				got, err = base64.StdEncoding.DecodeString(r.Body)
				if err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(got, test.wantBody) {
				t.Fatalf("body %v want %v", got, test.wantBody)
			}
		})
	}
}

func TestPanicAndOverflow(t *testing.T) {
	r, err := Adapt(context.Background(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Secret", "never")
		_, _ = w.Write([]byte("partial secret"))
		panic("private details")
	}), event())
	if err != nil || r.StatusCode != 500 || strings.Contains(r.Body, "secret") || r.Headers["secret"] != "" {
		t.Fatalf("panic response %#v, %v", r, err)
	}
	_, err = Adapt(context.Background(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, maxResponseBody+1)) }), event())
	if err == nil {
		t.Fatal("oversize response accepted")
	}
	_, err = Adapt(context.Background(), nil, event())
	if err == nil {
		t.Fatal("nil handler accepted")
	}
}

func TestCancellationAndStandardHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Adapt(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !errors.Is(r.Context().Err(), context.Canceled) {
			t.Error("lost cancellation")
		}
	}), event())
	if err != nil {
		t.Fatal(err)
	}
	e := event()
	e.RequestContext.HTTP.Method = "GET"
	r, err := Adapt(context.Background(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "style.css", time.Time{}, strings.NewReader("body {color: red}"))
	}), e)
	if err != nil || r.StatusCode != 200 || !strings.Contains(r.Body, "color") {
		t.Fatalf("ServeContent: %#v %v", r, err)
	}
}

func TestCompressedLargeAsset(t *testing.T) {
	// WASM can be larger than Lambda's body limit before compression. Verify
	// compression happens before buffering/capping and preserves the exact bytes.
	body := bytes.Repeat([]byte{0, 97, 115, 109, 255, 0, 1, 2}, 1024*1024)
	e := event()
	e.RequestContext.HTTP.Method = "GET"
	e.Headers["accept-encoding"] = "gzip"
	r, err := Adapt(context.Background(), httpx.Compression(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/wasm")
		_, _ = w.Write(body)
	})), e)
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsBase64Encoded || r.Headers["content-encoding"] != "gzip" || r.StatusCode != 200 {
		t.Fatalf("response: %#v", r)
	}
	compressed, err := base64.StdEncoding.DecodeString(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	roundtrip, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(roundtrip, body) {
		t.Fatalf("corrupt compressed asset: %v", err)
	}
}
