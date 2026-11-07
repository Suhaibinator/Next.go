package serverless

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
)

type invocationObservation struct {
	runtime, gateway string
	ctx              context.Context
}

func observeInvocation(ctx context.Context) invocationObservation {
	return invocationObservation{InvocationID(ctx), GatewayRequestID(ctx), ctx}
}
func requireInvocation(t *testing.T, got invocationObservation, runtime, gateway string) {
	t.Helper()
	if got.runtime != runtime || got.gateway != gateway {
		t.Errorf("identity got runtime=%q gateway=%q; want runtime=%q gateway=%q", got.runtime, got.gateway, runtime, gateway)
	}
}

func TestInvocationRuntimeWarmReusePreservesTransport(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(strconv.FormatBool(streaming), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			ids := []string{"runtime-one", "runtime-two", strings.Repeat("r", 257), "runtime-café", "runtime-final"}
			gateways := []string{"gateway-one", "gateway-two", "gateway-three", "gateway-four", ""}
			var nexts, reports atomic.Int32
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == runtimePrefix+"/invocation/next" {
					i := int(nexts.Add(1)) - 1
					if i >= len(ids) {
						w.WriteHeader(503)
						return
					}
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", ids[i])
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(2*time.Second).UnixMilli(), 10))
					e := afterEvent()
					e.RequestContext.RequestID = gateways[i]
					e.Headers["lambda-runtime-aws-request-id"] = "spoof"
					e.Headers["x-request-id"] = "spoof"
					_ = json.NewEncoder(w).Encode(e)
					return
				}
				i := int(reports.Add(1)) - 1
				if i >= len(ids) {
					t.Error("unexpected report")
					w.WriteHeader(500)
					return
				}
				want := runtimePrefix + "/invocation/" + url.PathEscape(ids[i]) + "/response"
				if r.URL.EscapedPath() != want {
					t.Errorf("transport ID changed: got %q want %q", r.URL.EscapedPath(), want)
				}
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					t.Error(err)
				}
				w.WriteHeader(202)
			}))
			defer runtime.Close()
			var captured []invocationObservation
			err := runWithOptions(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured = append(captured, observeInvocation(r.Context()))
				_, _ = io.WriteString(w, "ok")
			}), runtime.URL, runtime.Client(), runtimeOptions{streaming: streaming})
			if err == nil || ctx.Err() != nil || reports.Load() != int32(len(ids)) || len(captured) != len(ids) {
				t.Fatal("did not dispatch and route every legacy-valid ID", err, ctx.Err(), reports.Load(), len(captured))
			}
			for i, got := range captured {
				want := ids[i]
				if i == 2 || i == 3 {
					want = ""
				}
				requireInvocation(t, got, want, gateways[i])
				requireInvocation(t, observeInvocation(got.ctx), want, gateways[i])
			}
		})
	}
}

func TestInvocationRuntimeCancellationRetainsIdentity(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(strconv.FormatBool(streaming), func(t *testing.T) {
			parent, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			ctx, cancel := context.WithCancel(parent)
			defer cancel()
			entered := make(chan invocationObservation, 1)
			canceled := make(chan invocationObservation, 1)
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/next") {
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", "runtime-cancel")
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(2*time.Second).UnixMilli(), 10))
					e := afterEvent()
					e.RequestContext.RequestID = "gateway-cancel"
					_ = json.NewEncoder(w).Encode(e)
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(202)
			}))
			defer runtime.Close()
			done := make(chan error, 1)
			go func() {
				done <- runWithOptions(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					entered <- observeInvocation(r.Context())
					if streaming {
						w.(http.Flusher).Flush()
					}
					<-r.Context().Done()
					canceled <- observeInvocation(r.Context())
				}), runtime.URL, runtime.Client(), runtimeOptions{streaming: streaming})
			}()
			select {
			case got := <-entered:
				requireInvocation(t, got, "runtime-cancel", "gateway-cancel")
			case <-parent.Done():
				t.Fatal("handler did not enter")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-parent.Done():
				t.Fatal("runtime did not finish cancellation")
			}
			select {
			case got := <-canceled:
				requireInvocation(t, got, "runtime-cancel", "gateway-cancel")
				if !errors.Is(got.ctx.Err(), context.Canceled) {
					t.Fatal(got.ctx.Err())
				}
			default:
				t.Fatal("handler cancellation did not finish before runtime returned")
			}
		})
	}
}

func TestInvocationCompletionContextAndRejectedPublication(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, accepted := range []bool{false, true} {
			t.Run(strconv.FormatBool(streaming)+"/"+strconv.FormatBool(accepted), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				published := make(chan struct{})
				callback := make(chan invocationObservation, 1)
				var nexts atomic.Int32
				runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/next") {
						if nexts.Add(1) > 1 {
							select {
							case got := <-callback:
								requireInvocation(t, got, "runtime-completion", "gateway-completion")
							default:
								t.Error("next poll preceded accepted callback")
							}
							w.WriteHeader(503)
							return
						}
						w.Header().Set("Lambda-Runtime-Aws-Request-Id", "runtime-completion")
						w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(2*time.Second).UnixMilli(), 10))
						e := afterEvent()
						e.RequestContext.RequestID = "gateway-completion"
						_ = json.NewEncoder(w).Encode(e)
						return
					}
					_, _ = io.Copy(io.Discard, r.Body)
					if !accepted {
						w.WriteHeader(503)
						return
					}
					close(published)
					w.WriteHeader(202)
				}))
				defer runtime.Close()
				app, err := nextgo.New(nextgo.Config{Routes: []nextgo.Route{{Pattern: "/", API: func(w http.ResponseWriter, r *http.Request) {
					requireInvocation(t, observeInvocation(r.Context()), "runtime-completion", "gateway-completion")
					if err := nextgo.After(r.Context(), func(work context.Context) error {
						select {
						case <-published:
						default:
							t.Error("callback preceded publication")
						}
						if work.Err() != nil {
							t.Error(work.Err())
						}
						callback <- observeInvocation(work)
						return nil
					}); err != nil {
						t.Error(err)
					}
					_, _ = io.WriteString(w, "ok")
				}}}})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					drain, stop := context.WithTimeout(context.Background(), time.Second)
					defer stop()
					if err := app.Close(drain); err != nil {
						t.Error(err)
					}
				})
				err = runWithOptions(ctx, app, runtime.URL, runtime.Client(), runtimeOptions{streaming: streaming})
				if err == nil || ctx.Err() != nil {
					t.Fatal("expected finite mock rejection", err, ctx.Err())
				}
				if accepted {
					if nexts.Load() != 2 {
						t.Fatal("accepted callback did not drain before reuse")
					}
				} else {
					if nexts.Load() != 1 {
						t.Fatal("rejected transport reused invocation")
					}
					select {
					case <-callback:
						t.Fatal("rejected publication executed completion")
					default:
					}
				}
			})
		}
	}
}

func TestInvocationLegacyInvalidRoutingIDsStillReject(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, id := range []string{"", ".", "..", "a/b", "a\\b", "a?b", "a#b"} {
			t.Run(strconv.FormatBool(streaming)+"/"+strconv.Quote(id), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				var called atomic.Bool
				var reports atomic.Int32
				runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if !strings.HasSuffix(r.URL.Path, "/next") {
						reports.Add(1)
						w.WriteHeader(500)
						return
					}
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", id)
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(time.Minute).UnixMilli(), 10))
					_ = json.NewEncoder(w).Encode(afterEvent())
				}))
				defer runtime.Close()
				err := runWithOptions(ctx, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Store(true) }), runtime.URL, runtime.Client(), runtimeOptions{streaming: streaming})
				if err == nil || err.Error() != "serverless: missing or invalid invocation ID" || called.Load() || reports.Load() != 0 {
					t.Fatal("legacy routing rejection changed", err, called.Load(), reports.Load())
				}
			})
		}
	}
}
