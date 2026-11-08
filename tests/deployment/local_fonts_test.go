//go:build deployment

package deployment

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
)

var localFontLinkRE = regexp.MustCompile(`href="([^"]*/_nextgo/local-fonts/([a-f0-9]{64})\.(css|woff2?))"`)

func TestLocalFontsExecutable(t *testing.T) {
	for _, mode := range []string{"http", "buffered", "streaming"} {
		for _, scope := range []generatedActionScope{{}, {base: "/scoped", locale: "fr", slash: true}} {
			t.Run(mode+scope.prefix(), func(t *testing.T) {
				extra := []string{"NEXTGO_DEMO_CSP=true"}
				var worker *sessionExecutable
				if mode == "http" {
					worker = newSessionHTTP(t, scope, extra)
				} else {
					worker = newSessionLambda(t, mode, scope, extra)
				}
				defer worker.close()
				path := scope.prefix() + "/local-fonts"
				if scope.slash {
					path += "/"
				}
				page := worker.send(actionViewEvent{method: "GET", path: path, headers: make(http.Header)})
				if page.status != 200 || page.headers.Get(nextgo.RequiresServerHeader) == "1" || !bytes.Contains(page.data, []byte("Font bytes and declarations")) || !strings.Contains(page.headers.Get("Content-Security-Policy"), "default-src 'self'") {
					t.Fatal("font document/CSP/runtime dependency contract", page.status, page.headers, string(page.data))
				}
				links := localFontLinkRE.FindAllSubmatch(page.data, -1)
				if len(links) != 3 {
					t.Fatalf("expected one stylesheet and two explicit font preloads, got %d", len(links))
				}
				for _, link := range links {
					url, digest, format := string(link[1]), string(link[2]), string(link[3])
					if !strings.HasPrefix(url, scope.prefix()+"/_nextgo/local-fonts/") {
						t.Fatal("font resource lost locale/base ownership", url)
					}
					t.Run(format, func(t *testing.T) {
						reply := worker.send(actionViewEvent{method: "GET", path: url, headers: http.Header{"Accept-Encoding": {"identity"}}})
						sum := sha256.Sum256(reply.data)
						media := "font/" + format
						if format == "css" {
							media = "text/css; charset=utf-8"
							if bytes.Contains(reply.data, []byte("/_nextgo/")) || !bytes.Contains(reply.data, []byte(".woff2")) || !bytes.Contains(reply.data, []byte(".woff")) {
								t.Fatal("stylesheet must contain portable relative font filenames")
							}
							if !bytes.Contains(reply.data, []byte("font-display:swap")) {
								t.Fatal("font display policy missing")
							}
						} else {
							magic := "wOFF"
							if format == "woff2" {
								magic = "wOF2"
							}
							if !bytes.HasPrefix(reply.data, []byte(magic)) {
								t.Fatal("served font bytes do not match their format")
							}
						}
						if reply.status != 200 || hex.EncodeToString(sum[:]) != digest || reply.headers.Get("ETag") != `"`+digest+`"` || reply.headers.Get("Content-Type") != media || !strings.Contains(reply.headers.Get("Cache-Control"), "immutable") || reply.headers.Get("Access-Control-Allow-Origin") != "*" || reply.headers.Get("X-Content-Type-Options") != "nosniff" {
							t.Fatal("font/CSS content identity or cache policy", reply.status, reply.headers)
						}
						for _, method := range []string{"HEAD", "POST", "conditional"} {
							headers := http.Header{"Accept-Encoding": {"identity"}}
							verb, expected := method, 200
							if method == "conditional" {
								verb, expected = "GET", 304
								headers.Set("If-None-Match", `W/"`+digest+`"`)
							} else if method == "POST" {
								expected = 405
							}
							response := worker.send(actionViewEvent{method: verb, path: url, headers: headers})
							if response.status != expected || method != "POST" && len(response.data) != 0 {
								t.Fatal("font/CSS method/validator contract", method, response.status, string(response.data))
							}
						}
					})
				}
			})
		}
	}
}
