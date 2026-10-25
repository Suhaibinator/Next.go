//go:build deployment

package deployment

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/internal/navigation"
)

var viewportAnchorRE = regexp.MustCompile(`(?s)<a\b[^>]*>.*?</a>`)
var viewportBodyRE = regexp.MustCompile(`<body\b[^>]*>`)

func viewportAttribute(markup, name string) string {
	match := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `="([^"]*)"`).FindStringSubmatch(markup)
	if len(match) != 2 {
		return ""
	}
	return html.UnescapeString(match[1])
}
func viewportLinks(markup string) (map[string]string, error) {
	links := map[string]string{}
	for _, anchor := range viewportAnchorRE.FindAllString(markup, -1) {
		id := viewportAttribute(anchor, "id")
		if !strings.HasPrefix(id, "prefetch-") {
			continue
		}
		if links[id] != "" {
			return nil, fmt.Errorf("duplicate ui.Link fixture identity %s", id)
		}
		links[id] = anchor
	}
	for _, item := range []struct{ id, href, mode, label string }{
		{"prefetch-visible", "/posts?vp=visible", "viewport", "Visible automatic link"},
		{"prefetch-offscreen", "/posts?vp=offscreen", "viewport", "Offscreen automatic link"},
		{"prefetch-intent", "/posts?vp=intent", "intent", "Intent-only link"},
		{"prefetch-none", "/posts?vp=none", "false", "Disabled automatic link"},
		{"prefetch-legacy", "/posts?vp=legacy", "", "Legacy intent link"},
		{"prefetch-shell", "/partial/alpha?message=Viewport%20private", "viewport", "Public shell link"},
	} {
		anchor := links[item.id]
		if anchor == "" || viewportAttribute(anchor, "href") != item.href || viewportAttribute(anchor, "data-nextgo-prefetch") != item.mode || !strings.Contains(anchor, item.label) {
			return nil, fmt.Errorf("server-rendered native link %s lost its href, label, or mode", item.id)
		}
	}
	// These remain normal anchors. The browser's eligibility policy decides that
	// they cannot participate in viewport speculation or ordinary SPA reuse.
	for _, item := range []struct{ id, attribute, value string }{{"prefetch-external", "href", "https://example.invalid/prefetch"}, {"prefetch-api", "href", "/api/health"}, {"prefetch-download", "href", "/assets/style.css"}, {"prefetch-blank", "target", "_blank"}, {"prefetch-replace", "data-nextgo-replace", "true"}, {"prefetch-fragment", "href", "#prefetch-offscreen"}} {
		if viewportAttribute(links[item.id], item.attribute) != item.value {
			return nil, fmt.Errorf("ui.Link native attribute %s/%s changed", item.id, item.attribute)
		}
	}
	if !strings.Contains(links["prefetch-download"], `download=""`) || !strings.Contains(links["prefetch-reload"], `data-nextgo-reload=""`) {
		return nil, fmt.Errorf("download/reload browser semantics were removed")
	}
	if !strings.Contains(markup, `aria-label="Prefetch companion"`) || !strings.Contains(markup, `data-count=""`) || !strings.Contains(markup, `disabled=""`) {
		return nil, fmt.Errorf("useful pre-WASM counter markup missing")
	}
	return links, nil
}

type viewportDocument struct {
	configuration, build string
	manifest             navigation.Manifest
}

func viewportProductionDocument(data []byte) (viewportDocument, error) {
	var result viewportDocument
	markup := string(data)
	if !strings.Contains(markup, "Prefetch when a Go link is visible") || !strings.Contains(markup, "<title>Viewport prefetch — Next.go</title>") {
		return result, fmt.Errorf("meaningful ui.Link SSR missing")
	}
	if _, err := viewportLinks(markup); err != nil {
		return result, err
	}
	tag := viewportBodyRE.FindString(markup)
	if tag == "" {
		return result, fmt.Errorf("missing document bootstrap context")
	}
	if strings.Contains(tag, `data-nextgo-dev=`) || strings.Contains(tag, `data-nextgo-dev-generation=`) {
		return result, fmt.Errorf("production executable advertised development mode")
	}
	result.configuration, result.build = viewportAttribute(tag, "data-nextgo-configuration"), viewportAttribute(tag, "data-nextgo-build")
	if !navigation.ValidShellID(result.configuration) || result.build == "" {
		return result, fmt.Errorf("missing production configuration/build identity")
	}
	raw := viewportAttribute(tag, "data-nextgo-navigation")
	if err := json.Unmarshal([]byte(raw), &result.manifest); err != nil {
		return result, err
	}
	if result.manifest.Development {
		return result, fmt.Errorf("production viewport policy was disabled by development manifest")
	}
	present := false
	for _, route := range result.manifest.Routes {
		if route.Pattern == "/prefetch" && !route.Endpoint {
			present = true
		}
	}
	if !present || result.manifest.IsEndpoint("/posts", "/posts") || !result.manifest.IsEndpoint("/api/health", "/api/health") {
		return result, fmt.Errorf("viewport eligibility manifest disagrees with page/API route kinds")
	}
	runtime := "/_nextgo/" + result.build + "/"
	if !strings.Contains(markup, `src="`+runtime+`wasm_exec.js"`) || !strings.Contains(markup, `src="`+runtime+`boot.js"`) {
		return result, fmt.Errorf("ui.Link SSR lost matching public WASM bootstrap references")
	}
	return result, nil
}
func viewportNavigation(data []byte, document viewportDocument) error {
	var envelope nextgo.Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if envelope.Status != 200 || envelope.URL != "/prefetch" || envelope.Title != "Viewport prefetch — Next.go" || envelope.Build != document.build || envelope.Configuration != document.configuration {
		return fmt.Errorf("navigation escaped the production manifest/configuration")
	}
	_, err := viewportLinks(envelope.HTML)
	return err
}
func TestViewportLinksHTTPExecutable(t *testing.T) {
	addr := address(t)
	p := start(t, nil, "-addr", addr)
	defer p.stop(t)
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	origin := "http://" + addr
	waitPartialReady(t, client, p, origin)
	response, err := client.Get(origin + "/prefetch")
	if err != nil {
		t.Fatal(err)
	}
	data := body(t, response)
	if response.StatusCode != 200 {
		t.Fatal("ui.Link document status", response.StatusCode, string(data))
	}
	document, err := viewportProductionDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.Get(navigation.ConfigurationHeader) != document.configuration {
		t.Fatal("document manifest/configuration header mismatch")
	}
	// Follow the SSR href directly without any JavaScript. This validates that
	// opting into viewport work preserves ordinary direct URL navigation.
	response, err = client.Get(origin + "/posts?vp=visible")
	if err != nil {
		t.Fatal(err)
	}
	direct := body(t, response)
	if response.StatusCode != 200 || !bytes.Contains(direct, []byte("<!doctype html>")) || !bytes.Contains(direct, []byte("Next.go field notes")) {
		t.Fatal("ui.Link href needs the browser runtime to work", response.StatusCode, string(direct))
	}
	request, err := http.NewRequest("GET", origin+"/prefetch", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Nextgo-Navigate", "1")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	flight := body(t, response)
	if response.StatusCode != 200 || response.Header.Get(navigation.ConfigurationHeader) != document.configuration {
		t.Fatal("ui.Link navigation response identity changed", response.StatusCode, response.Header)
	}
	if err := viewportNavigation(flight, document); err != nil {
		t.Fatal(err)
	}
}
func TestViewportLinksLambdaExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			var issued, reports atomic.Int32
			done := make(chan struct{})
			results := make(chan partialShellHTTPReply, 3)
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/next") {
					index := int(issued.Add(1)) - 1
					if index >= 3 {
						if index == 3 {
							close(done)
						}
						<-r.Context().Done()
						return
					}
					path, query := "/prefetch", ""
					headers := map[string]string{"host": "viewport.test"}
					if index == 1 {
						headers["x-nextgo-navigate"] = "1"
					}
					if index == 2 {
						path, query = "/posts", "vp=visible"
					}
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(index))
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
					_ = json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": path, "rawQueryString": query, "headers": headers, "requestContext": map[string]any{"domainName": "viewport.test", "http": map[string]string{"method": "GET", "path": path}}})
					return
				}
				data, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
				if err != nil {
					t.Error(err)
				}
				var envelope httpEnvelope
				var payload []byte
				if mode == "buffered" {
					if err := json.Unmarshal(data, &envelope); err != nil {
						t.Error(err)
					}
					payload = []byte(envelope.Body)
					if envelope.Base64 {
						payload, err = base64.StdEncoding.DecodeString(envelope.Body)
						if err != nil {
							t.Error(err)
						}
					}
				} else {
					split := bytes.Index(data, make([]byte, 8))
					if split < 0 {
						t.Error("missing viewport Lambda metadata delimiter")
						w.WriteHeader(500)
						return
					}
					if err := json.Unmarshal(data[:split], &envelope); err != nil {
						t.Error(err)
					}
					payload = data[split+8:]
				}
				index := int(reports.Add(1)) - 1
				if index >= 3 {
					t.Error("extra viewport Lambda report")
					w.WriteHeader(500)
					return
				}
				headers := make(http.Header)
				for key, value := range envelope.Headers {
					headers.Set(key, value)
				}
				results <- partialShellHTTPReply{status: envelope.StatusCode, headers: headers, data: payload}
				w.WriteHeader(202)
			}))
			defer runtime.Close()
			p := start(t, []string{"AWS_LAMBDA_RUNTIME_API=" + strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE=" + mode})
			defer closeSeedChild(p)
			select {
			case <-done:
			case <-p.done:
				t.Fatalf("viewport worker stopped: %v\n%s", p.err, p.logs.String())
			case <-time.After(15 * time.Second):
				t.Fatal("viewport Lambda flow timeout", p.logs.String())
			}
			p.stop(t)
			if reports.Load() != 3 {
				t.Fatal("missing viewport Lambda reports", reports.Load())
			}
			documentReply, navigationReply, directReply := <-results, <-results, <-results
			for _, reply := range []partialShellHTTPReply{documentReply, navigationReply, directReply} {
				if reply.status != 200 {
					t.Fatal("viewport Lambda request status", reply.status, string(reply.data))
				}
			}
			document, err := viewportProductionDocument(documentReply.data)
			if err != nil {
				t.Fatal(err)
			}
			if documentReply.headers.Get(navigation.ConfigurationHeader) != document.configuration || navigationReply.headers.Get(navigation.ConfigurationHeader) != document.configuration {
				t.Fatal("Lambda lost production viewport configuration identity")
			}
			if err := viewportNavigation(navigationReply.data, document); err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(directReply.data, []byte("<!doctype html>")) || !bytes.Contains(directReply.data, []byte("Next.go field notes")) {
				t.Fatal("Lambda ui.Link target needs JavaScript to produce useful HTML")
			}
		})
	}
}
