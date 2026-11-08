//go:build deployment

package deployment

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
)

func TestWidgetsExecutable(t *testing.T) {
	binary := os.Getenv("NEXTGO_TEST_WIDGET_BINARY")
	if binary == "" {
		t.Skip("set NEXTGO_TEST_WIDGET_BINARY to the pinned widget example")
	}
	info, err := os.Lstat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 128<<20 {
		t.Fatal("invalid widget executable", err)
	}
	file, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.New()
	n, err := io.Copy(digest, io.LimitReader(file, info.Size()+1))
	closeErr := file.Close()
	if err != nil || closeErr != nil || n != info.Size() || hex.EncodeToString(digest.Sum(nil)) != os.Getenv("NEXTGO_TEST_WIDGET_SHA256") {
		t.Fatal("widget executable pin mismatch", err, closeErr)
	}
	t.Setenv("NEXTGO_TEST_BINARY", binary)
	for _, mode := range []string{"http", "buffered", "streaming"} {
		for _, base := range []string{"", "/scoped"} {
			t.Run(mode+base, func(t *testing.T) {
				extra := []string{"NEXTGO_METADATA_BASE_PATH=" + base, "NEXTGO_METADATA_ASSET_PREFIX=/widget-assets"}
				var worker *sessionExecutable
				if mode == "http" {
					worker = newSocialMetadataHTTP(t, extra, base+"/api/health")
				} else {
					worker = newSessionLambda(t, mode, generatedActionScope{}, extra)
				}
				defer worker.close()
				for _, path := range []string{"/", "/lazy", "/esm", "/lazy-esm", "/late", "/lazy-late", "/failure", "/about"} {
					t.Run(path, func(t *testing.T) {
						publicPath := base + path
						if base != "" && path == "/" {
							publicPath = base
						}
						for _, navigate := range []bool{false, true} {
							headers := make(http.Header)
							if navigate {
								headers.Set("X-Nextgo-Navigate", "1")
							}
							reply := worker.send(actionViewEvent{method: "GET", path: publicPath, headers: headers})
							if reply.status != 200 {
								t.Fatal("widget page status", reply.status, string(reply.data))
							}
							data := reply.data
							if navigate {
								var envelope nextgo.Envelope
								if json.Unmarshal(data, &envelope) != nil || envelope.Version != nextgo.NavigationProtocolVersion || envelope.Status != 200 || envelope.URL != publicPath {
									t.Fatal("widget navigation envelope", string(data))
								}
								data = []byte(envelope.HTML)
							}
							if path != "/about" && (!bytes.Contains(data, []byte(`data-widget-fallback=""`)) || !bytes.Contains(data, []byte("Sample data")) || bytes.Contains(data, []byte("<canvas"))) {
								t.Fatal("meaningful native widget fallback missing", string(data))
							}
						}
					})
				}
				for _, asset := range []struct{ name, sri, media string }{
					{"uPlot-1.6.32.iife.min.js", "Gx3t0zdBAuQOuvvmaLZj7HKEiSgWTAs+VdtNY7wt19QDPTDQjFIwAuXDj0zeN00c", "javascript"},
					{"uPlot-1.6.32.min.css", "IfV0B7MIOYuO95kO9G5ySKPz/85zqFNOAs8iy4tkK5zd9izhJAB8b7lHrwYqqmYE", "text/css"},
					{"uPlot-1.6.32.esm.js", "iUdBlEO5qc07+gOz+3rq/Besyud+KVIxM+xJEuUBObxLG3C/Dlkz8OYuPUcPrCGP", "javascript"},
					{"chart-module-v1.js", "fXaS6zRXr7cMB/MIcJRWvTrbukHBBZPePekjgXzIzrxkesF89otc8UxsTXN0okfg", "javascript"},
					{"settings-fixture-v1.js", "7TPwZ9iF3Yg//50tIua2SUpSdxK6gGsG5NMvP0iMwKY94KxIdgx5/Ldn060gKZKr", "javascript"},
					{"uPlot-LICENSE.txt", "LBMwZlHtMnim4BBA9gOI4OOPUI5cNjaxzIZCJRYYEyIykienqB4X7/vxq0t1Ihgd", "text/plain"},
				} {
					t.Run(asset.name, func(t *testing.T) {
						path := "/widget-assets/assets/vendor/" + asset.name
						reply := worker.send(actionViewEvent{method: "GET", path: path, headers: http.Header{"Accept-Encoding": {"identity"}}})
						sum := sha512.Sum384(reply.data)
						if reply.status != 200 || base64.StdEncoding.EncodeToString(sum[:]) != asset.sri || !strings.Contains(reply.headers.Get("Content-Type"), asset.media) {
							t.Fatal("vendor snapshot changed", asset.name, reply.status, reply.headers)
						}
						for _, method := range []string{"HEAD", "POST"} {
							headers, status := http.Header{"Accept-Encoding": {"identity"}}, 200
							if method == "POST" {
								status = 405
							}
							response := worker.send(actionViewEvent{method: method, path: path, headers: headers})
							if response.status != status || len(response.data) != 0 || method == "POST" && response.headers.Get("Allow") != "GET, HEAD" {
								t.Fatal("vendor HEAD/method contract", method, response.status)
							}
						}
					})
				}
			})
		}
	}
}
