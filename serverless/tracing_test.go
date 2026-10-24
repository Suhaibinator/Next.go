package serverless

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/telemetry"
)

func TestTracingFlushFollowsAcceptedCompletionBeforeNextInvocation(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, failFlush := range []bool{false, true} {
			t.Run(strconv.FormatBool(streaming)+"/"+strconv.FormatBool(failFlush), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				published := make(chan struct{})
				flushed := make(chan struct{})
				var once sync.Once
				var jobs atomic.Int32
				var nexts atomic.Int32
				var reports atomic.Int32
				tracer, err := telemetry.New(telemetry.Options{Recorder: telemetry.JSONRecorder(io.Discard), Flush: func(work context.Context) error {
					if jobs.Load() != 1 {
						t.Error("traces flushed before completion drained")
					}
					deadline, ok := work.Deadline()
					if !ok || time.Until(deadline) > 2*time.Second {
						t.Error("unbounded invocation flush")
					}
					once.Do(func() { close(flushed) })
					if failFlush {
						return errors.New("private collector failure")
					}
					return nil
				}})
				if err != nil {
					t.Fatal(err)
				}
				app, err := nextgo.New(nextgo.Config{Tracing: tracer, Routes: []nextgo.Route{{Pattern: "/", API: func(w http.ResponseWriter, r *http.Request) {
					if err := nextgo.After(r.Context(), func(context.Context) error {
						select {
						case <-published:
						default:
							t.Error("completion ran before acceptance")
						}
						jobs.Add(1)
						return nil
					}); err != nil {
						t.Error(err)
					}
					_, _ = io.WriteString(w, "public response")
				}}}})
				if err != nil {
					t.Fatal(err)
				}
				runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/next") {
						if nexts.Add(1) > 1 {
							select {
							case <-flushed:
							default:
								t.Error("runtime requested next before telemetry flush")
							}
							w.WriteHeader(503)
							return
						}
						w.Header().Set("Lambda-Runtime-Aws-Request-Id", "tracing")
						w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(2*time.Second).UnixMilli(), 10))
						_ = json.NewEncoder(w).Encode(afterEvent())
						return
					}
					_, _ = io.Copy(io.Discard, r.Body)
					reports.Add(1)
					close(published)
					w.WriteHeader(202)
				}))
				defer runtime.Close()
				err = runWithOptions(ctx, app, runtime.URL, runtime.Client(), runtimeOptions{streaming: streaming})
				if err == nil || reports.Load() != 1 || jobs.Load() != 1 {
					t.Fatal(err, reports.Load(), jobs.Load())
				}
				if failFlush && tracer.Stats().Failures != 1 {
					t.Fatal(tracer.Stats())
				}
				closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
				defer closeCancel()
				_ = app.Close(closeCtx)
			})
		}
	}
}
