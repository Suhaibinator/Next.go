//go:build deployment

package deployment

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

func TestNavigationStreamLambdaExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			var issued, reports atomic.Int32
			done := make(chan struct{})
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/next") {
					if issued.Add(1) > 1 {
						<-r.Context().Done()
						return
					}
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", "stream-navigation")
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
					json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": "/stream", "rawQueryString": "mode=nested", "headers": map[string]string{"host": "demo.test", "x-nextgo-navigate": "1", "x-nextgo-stream": "1"}, "requestContext": map[string]any{"domainName": "demo.test", "http": map[string]string{"method": "GET", "path": "/stream", "sourceIp": "127.0.0.1"}}})
					return
				}
				var response struct {
					StatusCode int               `json:"statusCode"`
					Headers    map[string]string `json:"headers"`
					Body       string            `json:"body"`
				}
				var payload io.Reader
				if mode == "buffered" {
					data, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
					if err != nil {
						t.Error(err)
					}
					if err := json.Unmarshal(data, &response); err != nil {
						t.Error(err)
					}
					payload = strings.NewReader(response.Body)
				} else {
					reader := bufio.NewReader(r.Body)
					var metadata []byte
					zeros := 0
					for len(metadata) < 64<<10 {
						value, err := reader.ReadByte()
						if err != nil {
							t.Error(err)
							break
						}
						metadata = append(metadata, value)
						if value == 0 {
							zeros++
						} else {
							zeros = 0
						}
						if zeros == 8 {
							metadata = metadata[:len(metadata)-8]
							break
						}
					}
					if err := json.Unmarshal(metadata, &response); err != nil {
						t.Error(err)
					}
					payload = reader
				}
				if response.StatusCode != 200 || response.Headers["x-nextgo-stream"] != "1" || !strings.Contains(response.Headers["content-type"], navigation.StreamMediaType) {
					t.Error("wrong Lambda navigation stream headers", response.StatusCode, response.Headers)
				}
				decoder := &navigation.StreamDecoder{}
				buffer := make([]byte, 1024)
				seen := map[string]bool{}
				var shell, slow time.Time
				consume := func(frame navigation.StreamFrame) error {
					switch frame.Type {
					case "shell":
						shell = time.Now()
						var page struct {
							HTML string `json:"html"`
							URL  string `json:"url"`
						}
						if err := json.Unmarshal(frame.Page, &page); err != nil {
							t.Error(err)
						}
						if !strings.Contains(page.HTML, "data-slow-pending") || page.URL != "/stream?mode=nested" {
							t.Error("invalid Lambda stream shell")
						}
					case "chunk":
						if frame.ID == "inner-boundary" && !seen["fast-boundary"] {
							t.Error("nested boundary preceded its parent")
						}
						seen[frame.ID] = true
						if frame.ID == "slow-boundary" {
							slow = time.Now()
						}
					}
					return nil
				}
				for {
					n, err := payload.Read(buffer)
					if n > 0 {
						if err := decoder.Feed(buffer[:n], consume); err != nil {
							t.Error(err)
							break
						}
					}
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Error(err)
						break
					}
				}
				if err := decoder.Finish(); err != nil {
					t.Error(err)
				}
				if len(seen) != 3 {
					t.Error("missing streamed boundary", seen)
				}
				if mode == "streaming" && (shell.IsZero() || slow.Sub(shell) < 700*time.Millisecond) {
					t.Error("Lambda transport buffered the navigation shell", slow.Sub(shell))
				}
				if reports.Add(1) == 1 {
					close(done)
				}
				w.WriteHeader(202)
			}))
			defer runtime.Close()
			p := start(t, []string{"AWS_LAMBDA_RUNTIME_API=" + strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE=" + mode})
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("Lambda navigation stream timeout", p.logs.String())
			}
			p.stop(t)
		})
	}
}
