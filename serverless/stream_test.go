package serverless

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/httpx"
	"github.com/Suhaibinator/Next.go/ui"
)

func readStreamPrelude(input io.Reader) (streamMetadata, *bufio.Reader, error) {
	reader := bufio.NewReader(input)
	var data []byte
	for len(data) < maxStreamPrelude {
		b, err := reader.ReadByte()
		if err != nil {
			return streamMetadata{}, reader, err
		}
		if b == 0 {
			var zeros [7]byte
			if _, err = io.ReadFull(reader, zeros[:]); err != nil {
				return streamMetadata{}, reader, err
			}
			if zeros != [7]byte{} {
				return streamMetadata{}, reader, fmt.Errorf("invalid metadata delimiter")
			}
			var metadata streamMetadata
			err = json.Unmarshal(data, &metadata)
			return metadata, reader, err
		}
		data = append(data, b)
	}
	return streamMetadata{}, reader, fmt.Errorf("unbounded metadata")
}

func TestStreamingDeferredFlushBeforeCompletion(t *testing.T) {
	for _, compress := range []bool{false, true} {
		t.Run(fmt.Sprint("gzip=", compress), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			release := make(chan struct{})
			early := make(chan struct{})
			handlerDone := make(chan struct{})
			observed := make(chan error, 1)
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fail := func(err error) { observed <- err; w.WriteHeader(500) }
				if r.Header.Get("Lambda-Runtime-Function-Response-Mode") != "streaming" || r.Header.Get("Content-Type") != "application/vnd.awslambda.http-integration-response" || len(r.TransferEncoding) != 1 || r.TransferEncoding[0] != "chunked" || !r.Close {
					fail(fmt.Errorf("incorrect stream transport headers: %#v", r))
					return
				}
				metadata, reader, err := readStreamPrelude(r.Body)
				if err != nil {
					fail(err)
					return
				}
				if metadata.StatusCode != 200 || len(metadata.Cookies) != 2 {
					fail(fmt.Errorf("metadata %#v", metadata))
					return
				}
				var body io.Reader = reader
				if compress {
					if metadata.Headers["content-encoding"] != "gzip" {
						fail(fmt.Errorf("missing gzip metadata"))
						return
					}
					decoded, err := gzip.NewReader(reader)
					if err != nil {
						fail(err)
						return
					}
					defer decoded.Close()
					body = decoded
				}
				shell := make([]byte, 5)
				if _, err = io.ReadFull(body, shell); err != nil || string(shell) != "shell" {
					fail(fmt.Errorf("initial shell: %s %v", shell, err))
					return
				}
				close(early)
				rest, err := io.ReadAll(body)
				if err != nil || !bytes.Contains(rest, []byte("loaded-content")) || !bytes.Contains(rest, []byte("data-nextgo-chunk")) {
					fail(fmt.Errorf("deferred chunks: %s %v", rest, err))
					return
				}
				observed <- nil
				w.WriteHeader(202)
			}))
			defer runtime.Close()
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(handlerDone)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Add("Set-Cookie", "one=1")
				w.Header().Add("Set-Cookie", "two=2")
				node := ui.Fragment(ui.Text("shell"), ui.Defer("slow", ui.Text("loading"), func(ctx context.Context) (ui.Node, error) {
					select {
					case <-release:
						return ui.Text("loaded-content"), nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}))
				_ = ui.RenderStream(r.Context(), w, node)
			})
			e := event()
			e.RequestContext.HTTP.Method = "GET"
			var handler http.Handler = h
			if compress {
				e.Headers["accept-encoding"] = "gzip"
				handler = httpx.Compression(handler)
			}
			done := make(chan error, 1)
			go func() {
				handled, err := streamInvocation(ctx, handler, e, runtime.URL, "one", runtime.Client(), maxStreamBody)
				if !handled && err == nil {
					err = fmt.Errorf("stream did not start")
				}
				done <- err
			}()
			select {
			case <-early:
			case err := <-observed:
				t.Fatalf("consumer before flush: %v", err)
			case <-ctx.Done():
				t.Fatal("no early shell flush")
			}
			select {
			case <-handlerDone:
				t.Fatal("initial shell arrived after handler completion")
			default:
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if err := <-observed; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStreamingMetadataAndBodySemantics(t *testing.T) {
	for _, test := range []struct {
		name, method string
		status       int
		body, want   []byte
	}{
		{"binary", "GET", 201, []byte{0, 255, 1, 2}, []byte{0, 255, 1, 2}},
		{"empty", "GET", 200, nil, nil},
		{"head", "HEAD", 200, []byte("not sent"), nil},
		{"204", "GET", 204, []byte("not sent"), nil},
		{"205", "GET", 205, []byte("not sent"), nil},
		{"304", "GET", 304, []byte("not sent"), nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			observed := make(chan error, 1)
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				metadata, reader, err := readStreamPrelude(r.Body)
				if err == nil {
					body, readErr := io.ReadAll(reader)
					err = readErr
					if !bytes.Equal(body, test.want) {
						err = fmt.Errorf("body %v expected %v", body, test.want)
					}
				}
				if err == nil && (metadata.StatusCode != test.status || metadata.Headers["vary"] != "Accept,Origin" || metadata.Headers["x-late"] != "" || metadata.Headers["x-hop"] != "" || len(metadata.Cookies) != 2) {
					err = fmt.Errorf("metadata %#v", metadata)
				}
				if err == nil {
					wantLength := ""
					if test.method == "HEAD" {
						wantLength = "123"
					}
					if metadata.Headers["content-length"] != wantLength {
						err = fmt.Errorf("incorrect content-length metadata: %#v", metadata)
					}
				}
				observed <- err
				w.WriteHeader(202)
			}))
			defer runtime.Close()
			e := event()
			e.RequestContext.HTTP.Method = test.method
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("Vary", "Accept")
				w.Header().Add("Vary", "Origin")
				w.Header().Add("Set-Cookie", "a=1")
				w.Header().Add("Set-Cookie", "b=2")
				w.Header().Set("Connection", "X-Hop")
				w.Header().Set("X-Hop", "discard")
				w.Header().Set("Content-Length", "123")
				w.WriteHeader(103)
				w.WriteHeader(test.status)
				w.Header().Set("X-Late", "ignored")
				_, _ = w.Write(test.body)
			})
			handled, err := streamInvocation(context.Background(), h, e, runtime.URL, "one", runtime.Client(), maxStreamBody)
			if err != nil || !handled {
				t.Fatalf("stream: %v %v", handled, err)
			}
			if err := <-observed; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStreamingPanicAndLimits(t *testing.T) {
	for _, test := range []struct {
		name       string
		handler    http.HandlerFunc
		limit      int64
		wantType   string
		wantStatus int
		wantBody   string
	}{
		{"early panic", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Secret", "discard")
			w.WriteHeader(201)
			panic("private information")
		}, 100, "", 500, "Internal Server Error\n"},
		{"early panic tiny cap", func(w http.ResponseWriter, r *http.Request) { panic("private information") }, 1, "", 500, ""},
		{"late panic", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("partial"))
			w.(http.Flusher).Flush()
			panic("private information")
		}, 100, "NextGo.HandlerPanic", 200, "partial"},
		{"late body cap", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("partial"))
			_, _ = w.Write([]byte("overflow"))
		}, 7, "NextGo.ResponseTooLarge", 200, "partial"},
	} {
		t.Run(test.name, func(t *testing.T) {
			observed := make(chan error, 1)
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				metadata, reader, err := readStreamPrelude(r.Body)
				if err == nil {
					body, readErr := io.ReadAll(reader)
					err = readErr
					if string(body) != test.wantBody || metadata.StatusCode != test.wantStatus || metadata.Headers["secret"] != "" {
						err = fmt.Errorf("unexpected body/status: %q %#v", body, metadata)
					}
				}
				if err == nil && r.Trailer.Get(streamErrorType) != test.wantType {
					err = fmt.Errorf("trailers %#v", r.Trailer)
				}
				if err == nil && test.wantType != "" {
					body, decodeErr := base64.StdEncoding.DecodeString(r.Trailer.Get(streamErrorBody))
					if decodeErr != nil || !json.Valid(body) || strings.Contains(string(body), "private") {
						err = fmt.Errorf("unsafe error trailer: %s %v", body, decodeErr)
					}
				}
				observed <- err
				w.WriteHeader(202)
			}))
			defer runtime.Close()
			handled, err := streamInvocation(context.Background(), test.handler, event(), runtime.URL, "one", runtime.Client(), test.limit)
			if !handled || err != nil {
				t.Fatalf("stream: %v %v", handled, err)
			}
			if err := <-observed; err != nil {
				t.Fatal(err)
			}
		})
	}

	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
		limit   int64
	}{
		{"prelude", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Large", strings.Repeat("a", maxStreamPrelude))
			_, _ = w.Write([]byte("body"))
		}, 100},
		{"initial body cap", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("long body")) }, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := make(chan struct{}, 1)
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls <- struct{}{}; w.WriteHeader(500) }))
			defer runtime.Close()
			handled, err := streamInvocation(context.Background(), test.handler, event(), runtime.URL, "one", runtime.Client(), test.limit)
			if handled || err == nil {
				t.Fatalf("must reject before sending response: %v %v", handled, err)
			}
			select {
			case <-calls:
				t.Fatal("sent response before validation")
			default:
			}
		})
	}
}

func TestStreamingConsumerFailureAndCancellation(t *testing.T) {
	t.Run("consumer rejects", func(t *testing.T) {
		runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
		defer runtime.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		handled, err := streamInvocation(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for i := 0; i < 100; i++ {
				if _, err := w.Write(make([]byte, 64*1024)); err != nil {
					return
				}
			}
		}), event(), runtime.URL, "one", runtime.Client(), maxStreamBody)
		if !handled || err == nil {
			t.Fatalf("consumer failure lost: %v %v", handled, err)
		}
	})
	t.Run("early consumer success is truncated", func(t *testing.T) {
		runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _, _ = readStreamPrelude(r.Body)
			w.WriteHeader(202)
			w.(http.Flusher).Flush()
		}))
		defer runtime.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		handled, err := streamInvocation(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.(http.Flusher).Flush(); <-r.Context().Done() }), event(), runtime.URL, "one", runtime.Client(), maxStreamBody)
		if !handled || err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("early response did not cancel handler: %v %v", handled, err)
		}
	})
	t.Run("deadline aborts pipe", func(t *testing.T) {
		started := make(chan struct{})
		runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _, _ = readStreamPrelude(r.Body)
			close(started)
			_, _ = io.Copy(io.Discard, r.Body)
		}))
		defer runtime.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := streamInvocation(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("shell"))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}), event(), runtime.URL, "one", runtime.Client(), maxStreamBody)
			done <- err
		}()
		<-started
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("stream did not cancel")
		}
	})
}

func TestStreamingEnvironment(t *testing.T) {
	t.Setenv("NEXTGO_LAMBDA_RESPONSE_MODE", "streaming")
	t.Setenv("NEXTGO_LAMBDA_STREAM_MAX_BYTES", "1024")
	options, err := optionsFromEnvironment()
	if err != nil || !options.streaming || options.maxStreamBytes != 1024 {
		t.Fatalf("options %#v %v", options, err)
	}
	for _, value := range []string{"0", "-1", "bogus", "200000001"} {
		t.Setenv("NEXTGO_LAMBDA_STREAM_MAX_BYTES", value)
		if _, err := optionsFromEnvironment(); err == nil {
			t.Fatalf("accepted cap %s", value)
		}
	}
	t.Setenv("NEXTGO_LAMBDA_STREAM_MAX_BYTES", "")
	t.Setenv("NEXTGO_LAMBDA_RESPONSE_MODE", "unknown")
	if _, err := optionsFromEnvironment(); err == nil {
		t.Fatal("accepted invalid mode")
	}
}

func TestRunStreamingMode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	early := make(chan struct{})
	release := make(chan struct{})
	consumed := make(chan error, 1)
	nextWait := make(chan struct{})
	var count atomic.Int32
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/next") {
			if count.Add(1) > 1 {
				close(nextWait)
				<-r.Context().Done()
				return
			}
			w.Header().Set("Lambda-Runtime-Aws-Request-Id", "runstream")
			w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(time.Minute).UnixMilli(), 10))
			e := event()
			e.Headers["accept-encoding"] = "gzip"
			_ = json.NewEncoder(w).Encode(e)
			return
		}
		metadata, reader, err := readStreamPrelude(r.Body)
		if err != nil {
			consumed <- err
			w.WriteHeader(500)
			return
		}
		if metadata.Headers["content-encoding"] != "gzip" || r.Header.Get("Lambda-Runtime-Function-Response-Mode") != "streaming" {
			consumed <- fmt.Errorf("streaming mode not enabled: %#v", metadata)
			w.WriteHeader(500)
			return
		}
		decoded, err := gzip.NewReader(reader)
		if err != nil {
			consumed <- err
			w.WriteHeader(500)
			return
		}
		defer decoded.Close()
		shell := make([]byte, 5)
		_, err = io.ReadFull(decoded, shell)
		if err != nil || string(shell) != "shell" {
			consumed <- fmt.Errorf("no early shell %q %v", shell, err)
			w.WriteHeader(500)
			return
		}
		close(early)
		body, err := io.ReadAll(decoded)
		if err == nil && string(body) != "final" {
			err = fmt.Errorf("body %q", body)
		}
		consumed <- err
		w.WriteHeader(202)
	}))
	defer runtime.Close()
	t.Setenv("AWS_LAMBDA_RUNTIME_API", strings.TrimPrefix(runtime.URL, "http://"))
	t.Setenv("NEXTGO_LAMBDA_RESPONSE_MODE", "streaming")
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("shell"))
			w.(http.Flusher).Flush()
			select {
			case <-release:
				_, _ = w.Write([]byte("final"))
			case <-r.Context().Done():
			}
		}))
	}()
	select {
	case <-early:
	case err := <-consumed:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("Run did not stream initial data")
	}
	close(release)
	if err := <-consumed; err != nil {
		t.Fatal(err)
	}
	select {
	case <-nextWait:
	case <-ctx.Done():
		t.Fatal("did not complete streaming invocation")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run stream shutdown: %v", err)
	}
}

func TestStreamingInvocationDeadline(t *testing.T) {
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _, _ = readStreamPrelude(r.Body)
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer runtime.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	handled, err := streamInvocation(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.(http.Flusher).Flush(); <-r.Context().Done() }), event(), runtime.URL, "deadline", runtime.Client(), maxStreamBody)
	if !handled || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline stream: %v %v", handled, err)
	}
}

func TestStreamingPreludeFailureUsesInvocationError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var handled atomic.Bool
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/next") {
			w.Header().Set("Lambda-Runtime-Aws-Request-Id", "largeheaders")
			w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(time.Minute).UnixMilli(), 10))
			_ = json.NewEncoder(w).Encode(event())
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/largeheaders/error") {
			t.Errorf("sent a response after prelude failure: %s", r.URL.Path)
		}
		data, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(data), "prelude exceeds") {
			t.Errorf("unexpected error body: %s", data)
		}
		handled.Store(true)
		w.WriteHeader(202)
		cancel()
	}))
	defer runtime.Close()
	err := runWithOptions(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Large", strings.Repeat("x", maxStreamPrelude))
		_, _ = w.Write([]byte("body"))
	}), runtime.URL, runtime.Client(), runtimeOptions{streaming: true})
	if !handled.Load() || !errors.Is(err, context.Canceled) {
		t.Fatalf("invocation error path: %v %v", handled.Load(), err)
	}
}
