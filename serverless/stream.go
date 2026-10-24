package serverless

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const maxStreamPrelude = 16 * 1024
const maxStreamBody int64 = 200_000_000
const streamErrorType = "Lambda-Runtime-Function-Error-Type"
const streamErrorBody = "Lambda-Runtime-Function-Error-Body"

type runtimeOptions struct {
	streaming      bool
	maxStreamBytes int64
}

func optionsFromEnvironment() (runtimeOptions, error) {
	options := runtimeOptions{maxStreamBytes: maxStreamBody}
	switch os.Getenv("NEXTGO_LAMBDA_RESPONSE_MODE") {
	case "", "buffered":
	case "streaming":
		options.streaming = true
	default:
		return options, fmt.Errorf("serverless: NEXTGO_LAMBDA_RESPONSE_MODE must be buffered or streaming")
	}
	if value := os.Getenv("NEXTGO_LAMBDA_STREAM_MAX_BYTES"); value != "" {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 1 || n > maxStreamBody {
			return options, fmt.Errorf("serverless: NEXTGO_LAMBDA_STREAM_MAX_BYTES must be 1..%d", maxStreamBody)
		}
		options.maxStreamBytes = n
	}
	return options, nil
}

type streamMetadata struct {
	StatusCode int               `json:"statusCode"`
	Headers    map[string]string `json:"headers,omitempty"`
	Cookies    []string          `json:"cookies,omitempty"`
}

// streamInvocation returns handled once an HTTP response POST has started. The
// caller must never also report an invocation error after that point.
func streamInvocation(ctx context.Context, handler http.Handler, event Request, endpoint, id string, client *http.Client, limit int64) (handled bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := event.HTTPRequest(ctx)
	if err != nil {
		return false, err
	}
	defer req.Body.Close()
	if limit <= 0 || limit > maxStreamBody {
		limit = maxStreamBody
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	stop := context.AfterFunc(ctx, func() { _ = reader.CloseWithError(ctx.Err()); _ = writer.CloseWithError(ctx.Err()) })
	defer stop()
	result := make(chan error, 1)
	uploadDone := make(chan struct{})
	trailers := http.Header{streamErrorType: nil, streamErrorBody: nil}
	w := &streamResponseWriter{header: make(http.Header), pipe: writer, ctx: ctx, method: req.Method, limit: limit}
	w.start = func() error {
		post, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+runtimePrefix+"/invocation/"+url.PathEscape(id)+"/response", reader)
		if err != nil {
			return err
		}
		post.Header.Set("Content-Type", "application/vnd.awslambda.http-integration-response")
		post.Header.Set("Lambda-Runtime-Function-Response-Mode", "streaming")
		post.TransferEncoding = []string{"chunked"}
		post.ContentLength = -1
		post.Close = true
		post.Trailer = trailers
		go func() {
			response, err := client.Do(post)
			if err == nil {
				_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
				response.Body.Close()
				if response.StatusCode < 200 || response.StatusCode >= 300 {
					err = fmt.Errorf("stream response HTTP %d", response.StatusCode)
				} else if readErr != nil {
					err = readErr
				}
			}
			select {
			case <-uploadDone:
			default:
				if err == nil {
					err = fmt.Errorf("Runtime API responded before the upload completed")
				}
			}
			// The Runtime API may reject an upload before consuming it. Closing
			// the reader and canceling the handler prevents blocked pipe writes.
			if err != nil {
				_ = reader.CloseWithError(err)
				cancel()
			}
			result <- err
		}()
		return nil
	}
	func() {
		defer func() {
			if failure := recover(); failure != nil {
				log.Printf("nextgo serverless: streaming handler panic (%T)", failure)
				if w.committed {
					w.failure = "NextGo.HandlerPanic"
					return
				}
				w.header = make(http.Header)
				w.frozen = nil
				w.status = 0
				w.err = nil
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(500)
				body := []byte("Internal Server Error\n")
				if int64(len(body)) <= w.limit {
					_, _ = w.Write(body)
				}
			}
		}()
		handler.ServeHTTP(w, req)
	}()
	if !w.committed && w.err == nil {
		w.commit(nil)
	}
	if !w.committed {
		return false, w.err
	}
	if w.failure != "" {
		trailers.Set(streamErrorType, w.failure)
		payload, _ := json.Marshal(map[string]string{"errorType": w.failure, "errorMessage": "The response stream failed"})
		trailers.Set(streamErrorBody, base64.StdEncoding.EncodeToString(payload))
	}
	close(uploadDone)
	_ = writer.Close()
	select {
	case err := <-result:
		if err != nil {
			return true, fmt.Errorf("serverless: streaming response: %w", err)
		}
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		if w.err != nil && w.failure == "" {
			return true, w.err
		}
		return true, nil
	case <-ctx.Done():
		return true, ctx.Err()
	}
}

type streamResponseWriter struct {
	header, frozen http.Header
	pipe           *io.PipeWriter
	ctx            context.Context
	status         int
	method         string
	limit, written int64
	committed      bool
	err            error
	failure        string
	start          func() error
}

func (w *streamResponseWriter) Header() http.Header { return w.header }
func (w *streamResponseWriter) WriteHeader(status int) {
	if status < 100 || status > 999 {
		panic("invalid HTTP status code")
	}
	if w.status != 0 || w.committed {
		return
	}
	if status < 200 {
		return
	}
	w.status = status
	w.frozen = w.header.Clone()
}
func (w *streamResponseWriter) commit(sample []byte) {
	if w.committed || w.err != nil {
		return
	}
	if err := w.ctx.Err(); err != nil {
		w.err = err
		return
	}
	if w.status == 0 {
		w.WriteHeader(200)
	}
	metadata := streamMetadata{StatusCode: w.status, Headers: make(map[string]string)}
	for name, values := range w.frozen {
		if strings.EqualFold(name, "Set-Cookie") {
			metadata.Cookies = append(metadata.Cookies, values...)
			continue
		}
		if hopHeader(name) || (strings.EqualFold(name, "Content-Length") && w.method != "HEAD") || strings.HasPrefix(name, http.TrailerPrefix) {
			continue
		}
		skip := false
		for _, n := range strings.Split(w.frozen.Get("Connection"), ",") {
			if strings.EqualFold(name, strings.TrimSpace(n)) {
				skip = true
			}
		}
		if !skip {
			metadata.Headers[strings.ToLower(name)] = strings.Join(values, ",")
		}
	}
	if len(sample) > 0 && metadata.Headers["content-type"] == "" && metadata.Headers["content-encoding"] == "" {
		metadata.Headers["content-type"] = http.DetectContentType(sample)
	}
	prelude, err := json.Marshal(metadata)
	if err != nil {
		w.err = err
		return
	}
	if len(prelude)+8 > maxStreamPrelude {
		w.err = fmt.Errorf("stream response prelude exceeds %d bytes", maxStreamPrelude)
		return
	}
	if err = w.start(); err != nil {
		w.err = err
		return
	}
	w.committed = true
	prelude = append(prelude, make([]byte, 8)...)
	_, w.err = w.pipe.Write(prelude)
}
func (w *streamResponseWriter) Write(body []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if err := w.ctx.Err(); err != nil {
		w.err = err
		return 0, err
	}
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if w.status == 204 || w.status == 205 || w.status == 304 {
		return 0, http.ErrBodyNotAllowed
	}
	if w.method == "HEAD" {
		return len(body), nil
	}
	if len(body) == 0 {
		return 0, nil
	}
	if int64(len(body)) > w.limit-w.written {
		w.err = fmt.Errorf("stream response exceeds %d bytes", w.limit)
		if w.committed {
			w.failure = "NextGo.ResponseTooLarge"
		}
		return 0, w.err
	}
	w.commit(body)
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.pipe.Write(body)
	w.written += int64(n)
	if err != nil {
		w.err = err
	}
	return n, err
}

// Flush commits the integration metadata immediately. Each subsequent pipe
// write becomes an HTTP/1.1 chunk; there is no intermediate response buffer.
func (w *streamResponseWriter) Flush() { w.commit(nil) }
