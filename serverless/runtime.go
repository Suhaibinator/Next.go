package serverless

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Suhaibinator/Next.go/httpx"
	"github.com/Suhaibinator/Next.go/internal/postresponse"
)

const runtimePrefix = "/2018-06-01/runtime"

// Run serves consecutive Lambda invocations until ctx is canceled or the
// Runtime API fails. Set AWS_LAMBDA_RUNTIME_API to the runtime's host:port.
// HTTP API v2 responses are buffered by default. Set
// NEXTGO_LAMBDA_RESPONSE_MODE=streaming for response-streaming integrations.
func Run(ctx context.Context, handler http.Handler) error {
	if handler == nil {
		return fmt.Errorf("serverless: nil handler")
	}
	endpoint, err := runtimeEndpoint(os.Getenv("AWS_LAMBDA_RUNTIME_API"))
	if err != nil {
		return err
	}
	options, err := optionsFromEnvironment()
	if err != nil {
		return err
	}
	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		baseTransport = &http.Transport{}
	}
	transport := baseTransport.Clone()
	transport.Proxy = nil // The local Runtime API must never pass through an HTTP proxy.
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	compressed := httpx.Compression(handler)
	if flusher, ok := handler.(telemetryFlusher); ok {
		compressed = flushingHandler{compressed, flusher}
	}
	return runWithOptions(ctx, compressed, endpoint, client, options)
}

type telemetryFlusher interface{ FlushTelemetry(context.Context) error }
type flushingHandler struct {
	http.Handler
	telemetryFlusher
}

func runtimeEndpoint(address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || strings.ContainsAny(host, "/\\?#@ \t\r\n") {
		return "", fmt.Errorf("serverless: AWS_LAMBDA_RUNTIME_API must be host:port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("serverless: invalid Runtime API port")
	}
	u := url.URL{Scheme: "http", Host: address}
	return u.String(), nil
}

func run(ctx context.Context, handler http.Handler, endpoint string, client *http.Client) error {
	return runWithOptions(ctx, handler, endpoint, client, runtimeOptions{})
}

func runWithOptions(ctx context.Context, handler http.Handler, endpoint string, client *http.Client, options runtimeOptions) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+runtimePrefix+"/invocation/next", nil)
		if err != nil {
			return err
		}
		next, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("serverless: next invocation: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(next.Body, 8*1024*1024+1))
		next.Body.Close()
		if next.StatusCode != 200 {
			return fmt.Errorf("serverless: next invocation HTTP %d", next.StatusCode)
		}
		id := next.Header.Get("Lambda-Runtime-Aws-Request-Id")
		if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\?#\r\n") {
			return fmt.Errorf("serverless: missing or invalid invocation ID")
		}
		invocationErr := readErr
		if len(body) > 8*1024*1024 {
			invocationErr = fmt.Errorf("invocation payload too large")
		}
		deadlineMS, deadlineErr := strconv.ParseInt(next.Header.Get("Lambda-Runtime-Deadline-Ms"), 10, 64)
		if deadlineErr != nil || deadlineMS <= 0 {
			invocationErr = fmt.Errorf("missing or invalid invocation deadline")
		}
		var event Request
		if invocationErr == nil {
			invocationErr = json.Unmarshal(body, &event)
		}
		if err := completeInvocation(ctx, handler, endpoint, client, options, id, deadlineMS, event, invocationErr); err != nil {
			return err
		}
	}
}

func completeInvocation(ctx context.Context, handler http.Handler, endpoint string, client *http.Client, options runtimeOptions, id string, deadlineMS int64, event Request, invocationErr error) error {
	ctx = withInvocationID(ctx, id)
	ctx = withGatewayRequestID(ctx, event.RequestContext.RequestID)
	invokeCtx, cancelInvocation := context.WithDeadline(ctx, time.UnixMilli(deadlineMS))
	defer cancelInvocation()
	invokeCtx, collector := postresponse.Collect(invokeCtx)
	defer func() {
		collector.Finish(invokeCtx, false) // transport failures must release reservations
		if flusher, ok := handler.(telemetryFlusher); ok {
			flushCtx, cancel := context.WithTimeout(invokeCtx, 2*time.Second)
			defer cancel()
			if err := flusher.FlushTelemetry(flushCtx); err != nil {
				log.Printf("serverless: telemetry flush failed (%T)", err)
			}
		}
	}()
	var response Response
	if invocationErr == nil {
		if options.streaming {
			handled, streamErr := streamInvocation(invokeCtx, handler, event, endpoint, id, client, options.maxStreamBytes)
			if handled {
				if streamErr == nil {
					collector.Finish(invokeCtx, true)
				}
				return streamErr
			}
			invocationErr = streamErr
		} else {
			response, invocationErr = Adapt(invokeCtx, handler, event)
			if invocationErr == nil && invokeCtx.Err() != nil {
				invocationErr = invokeCtx.Err()
			}
		}
	}
	kind := "response"
	var payload []byte
	var err error
	if invocationErr != nil {
		kind = "error"
		payload, _ = json.Marshal(struct {
			Message string `json:"errorMessage"`
			Type    string `json:"errorType"`
		}{invocationErr.Error(), "NextGoInvocationError"})
	} else {
		payload, err = json.Marshal(response)
		if err != nil {
			return err
		}
		if len(payload) > 6*1024*1024 {
			invocationErr = fmt.Errorf("HTTP response envelope exceeds Lambda payload limit")
			kind = "error"
			payload = []byte(`{"errorMessage":"HTTP response envelope exceeds Lambda payload limit","errorType":"NextGoInvocationError"}`)
		}
	}
	// Reporting can outlive the invocation deadline, but remains bounded and
	// stops immediately when the runtime itself is shutting down.
	reportCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reportCtx, http.MethodPost, endpoint+runtimePrefix+"/invocation/"+url.PathEscape(id)+"/"+kind, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if invocationErr != nil {
		req.Header.Set("Lambda-Runtime-Function-Error-Type", "Unhandled")
	}
	result, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("serverless: report invocation %s: %w", kind, err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(result.Body, 64*1024))
	result.Body.Close()
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return fmt.Errorf("serverless: report invocation %s HTTP %d", kind, result.StatusCode)
	}
	collector.Finish(invokeCtx, kind == "response")
	return nil
}
