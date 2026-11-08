//go:build deployment

package deployment

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newSocialMetadataHTTP(t *testing.T, extra []string, readyPath string) *sessionExecutable {
	t.Helper()
	addr := address(t)
	p := start(t, extra, "-addr", addr)
	client := &http.Client{Timeout: 7 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for deadline := time.Now().Add(7 * time.Second); ; {
		response, err := client.Get("http://" + addr + readyPath)
		if err == nil {
			_ = body(t, response)
			if response.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("metadata readiness timeout", p.logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	var closed bool
	closeWorker := func() {
		if !closed {
			closed = true
			client.CloseIdleConnections()
			p.stop(t)
		}
	}
	t.Cleanup(func() { client.CloseIdleConnections() })
	return &sessionExecutable{origin: "http://untrusted.invalid", close: closeWorker, send: func(event actionViewEvent) generatedActionReply {
		request, err := http.NewRequest(event.method, "http://"+addr+event.path, strings.NewReader(event.payload))
		if err != nil {
			t.Fatal(err)
		}
		request.Host = "untrusted.invalid"
		request.Header = event.headers.Clone()
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return generatedActionReply{response.StatusCode, response.Header.Clone(), body(t, response)}
	}}
}

func TestSocialMetadataExecutable(t *testing.T) {
	exerciseSocialImageFiles(t, "NEXTGO_TEST_METADATA_BINARY", []string{"opengraph-image.png", "twitter-image.png", "opengraph-image.alt.txt", "twitter-image.alt.txt"}, 1200, 630)
}

func TestSocialVariantsExecutable(t *testing.T) {
	exerciseSocialImageFiles(t, "NEXTGO_TEST_VARIANTS_BINARY", []string{"opengraph-image.jpg", "twitter-image.gif", "opengraph-image.alt.txt", "twitter-image.alt.txt"}, 320, 180)
}

func exerciseSocialImageFiles(t *testing.T, binarySetting string, names []string, width, height int) {
	t.Helper()
	binary := os.Getenv(binarySetting)
	if binary == "" {
		t.Skip("set " + binarySetting + " to the corresponding built example")
	}
	t.Setenv("NEXTGO_TEST_BINARY", binary)
	for _, mode := range []string{"http", "buffered", "streaming"} {
		for _, scope := range []generatedActionScope{{}, {base: "/scoped", slash: true}} {
			t.Run(mode+scope.base, func(t *testing.T) {
				extra := []string{"NEXTGO_METADATA_BASE_PATH=" + scope.base, "NEXTGO_METADATA_ASSET_PREFIX=/separate-assets", "NEXTGO_METADATA_TRAILING_SLASH=" + strconv.FormatBool(scope.slash)}
				root := scope.base
				if root == "" || scope.slash {
					root += "/"
				}
				var worker *sessionExecutable
				if mode == "http" {
					worker = newSocialMetadataHTTP(t, extra, root)
				} else {
					worker = newSessionLambda(t, mode, generatedActionScope{}, extra)
				}
				defer worker.close()
				page := worker.send(actionViewEvent{method: "GET", path: root, headers: http.Header{"Host": {"untrusted.invalid"}}})
				if page.status != 200 || page.headers.Get("X-Nextgo-Requires-Server") == "1" {
					t.Fatal("social metadata document requires runtime content", page.status, page.headers)
				}
				head, _, _ := bytes.Cut(page.data, []byte("</head>"))
				for _, name := range names[:2] {
					if !bytes.Contains(head, []byte("https://example.test"+scope.base+"/"+name)) {
						t.Fatal("missing trusted global image metadata", string(head))
					}
				}
				if bytes.Contains(head, []byte("untrusted.invalid")) || bytes.Contains(head, []byte("/separate-assets/opengraph")) {
					t.Fatal("inferred request/CDN image origin", string(head))
				}
				for _, name := range names {
					t.Run(name, func(t *testing.T) {
						path := scope.base + "/" + name
						reply := worker.send(actionViewEvent{method: "GET", path: path, headers: http.Header{"Accept-Encoding": {"identity"}, "Cookie": {"private=ignored"}}})
						digest := sha256.Sum256(reply.data)
						etag := `"` + hex.EncodeToString(digest[:]) + `"`
						media := "text/plain; charset=utf-8"
						if !strings.HasSuffix(name, ".txt") {
							expected := "png"
							if strings.HasSuffix(name, ".jpg") {
								expected = "jpeg"
							} else if strings.HasSuffix(name, ".gif") {
								expected = "gif"
							}
							decoded, format, err := image.Decode(bytes.NewReader(reply.data))
							if err != nil || format != expected || decoded.Bounds().Dx() != width || decoded.Bounds().Dy() != height {
								t.Fatal("invalid actual raster response", format, err)
							}
							media = "image/" + expected
							if expected == "gif" {
								animation, err := gif.DecodeAll(bytes.NewReader(reply.data))
								if err != nil || len(animation.Image) != 2 {
									t.Fatal("animation frames lost", err)
								}
							}
						}
						if reply.status != 200 || reply.headers.Get("Content-Type") != media || reply.headers.Get("ETag") != etag || reply.headers.Get("Cache-Control") != "public, max-age=0, must-revalidate" || reply.headers.Get("X-Content-Type-Options") != "nosniff" || reply.headers.Get("X-Nextgo-Build") == "" || len(reply.headers.Values("Set-Cookie")) != 0 {
							t.Fatal("social resource identity/privacy", reply.status, reply.headers)
						}
						for _, method := range []string{"HEAD", "POST", "conditional"} {
							headers := http.Header{"Accept-Encoding": {"identity"}}
							verb, status := method, 200
							if method == "conditional" {
								verb, status = "GET", 304
								headers.Set("If-None-Match", "W/"+etag)
							} else if method == "POST" {
								status = 405
							}
							response := worker.send(actionViewEvent{method: verb, path: path, headers: headers})
							if response.status != status || method != "POST" && len(response.data) != 0 {
								t.Fatal("social resource method/validator", method, response.status)
							}
							if method == "HEAD" && (response.headers.Get("ETag") != etag || response.headers.Get("Content-Length") != strconv.Itoa(len(reply.data))) {
								t.Fatal("HEAD representation mismatch")
							}
						}
					})
				}
			})
		}
	}
}
