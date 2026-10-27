package shared

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Cancellation at EOF happens after Do has returned a valid response. ReadAll
// treats that EOF as success, so this deterministically exercises the final
// acknowledgement without racing a network cancellation or sleeping.
func TestHTTPCancellationAtSuccessfulResponseEOFDoesNotAcknowledgeSuccess(t *testing.T) {
	lease := strings.Repeat("a", 64)
	selected := View{Lease: lease, LeaseFor: time.Second}
	published := View{Found: true, Value: []byte("published"), FreshFor: time.Second, ExpiresIn: time.Hour}
	operations := []struct {
		name     string
		response wireResponse
		want     View
		call     func(context.Context, *HTTP) (View, error)
	}{
		{"begin", wireResponse{View: &selected}, selected, func(ctx context.Context, client *HTTP) (View, error) {
			return client.Begin(ctx, testID, testPolicy, time.Second)
		}},
		{"commit", wireResponse{View: &published}, published, func(ctx context.Context, client *HTTP) (View, error) {
			return client.Commit(ctx, testID, lease, []byte("published"))
		}},
		{"release", wireResponse{}, View{}, func(ctx context.Context, client *HTTP) (View, error) {
			return View{}, client.Release(ctx, testID, lease)
		}},
	}
	for _, operation := range operations {
		for _, mode := range []string{"success", "cancel", "private-cause"} {
			t.Run(operation.name+"/"+mode, func(t *testing.T) {
				response := operation.response
				response.Version = protocol
				data, err := json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancelCause(context.Background())
				defer cancel(nil)
				cause := errors.New("private caller cancellation detail")
				body := &httpCancellationEOFBody{reader: bytes.NewReader(data)}
				if mode != "success" {
					body.cancel = func() {
						if mode == "private-cause" {
							cancel(cause)
						} else {
							cancel(nil)
						}
					}
				}
				transport := &httpCancellationTransport{body: body}
				client, err := NewHTTP("http://127.0.0.1/cache", secret, &http.Client{Transport: transport})
				if err != nil {
					t.Fatal(err)
				}
				got, err := operation.call(ctx, client)
				if !body.eof || !body.closed || transport.calls != 1 {
					t.Fatal("response did not reach EOF/close or request was retried", body.eof, body.closed, transport.calls)
				}
				if mode == "success" {
					if err != nil || ctx.Err() != nil || !reflect.DeepEqual(got, operation.want) {
						t.Fatal("valid uncanceled response changed", got, err)
					}
				} else {
					if err != context.Canceled || !reflect.DeepEqual(got, View{}) || ctx.Err() != context.Canceled {
						t.Fatal("canceled successful response was acknowledged", got, err)
					}
					if mode == "private-cause" && context.Cause(ctx) != cause {
						t.Fatal("fixture lost its distinct cause")
					}
				}
			})
		}
	}
}

type httpCancellationEOFBody struct {
	reader      *bytes.Reader
	cancel      func()
	eof, closed bool
}

func (body *httpCancellationEOFBody) Read(value []byte) (int, error) {
	n, err := body.reader.Read(value)
	if err == io.EOF {
		body.eof = true
		if body.cancel != nil {
			cancel := body.cancel
			body.cancel = nil
			cancel()
		}
	}
	return n, err
}

func (body *httpCancellationEOFBody) Close() error {
	body.closed = true
	return nil
}

type httpCancellationTransport struct {
	body  io.ReadCloser
	calls int
}

func (transport *httpCancellationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.calls++
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: transport.body, Request: request}, nil
}
