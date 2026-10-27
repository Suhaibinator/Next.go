package serverless

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestInvocationMetadataBoundsAndOwnership(t *testing.T) {
	for _, ctx := range []context.Context{nil, context.Background()} {
		if InvocationID(ctx) != "" || GatewayRequestID(ctx) != "" {
			t.Fatal("metadata appeared without a platform context")
		}
	}
	for _, test := range []struct {
		name, id string
		valid    bool
	}{
		{"one", "a", true}, {"255", strings.Repeat("x", 255), true}, {"256", strings.Repeat("x", 256), true},
		{"257", strings.Repeat("x", 257), false}, {"empty", "", false}, {"space", "a b", false}, {"tab", "a\tb", false},
		{"nul", "a\x00b", false}, {"newline", "a\nb", false}, {"delete", "a\x7fb", false}, {"unicode", "café", false},
		{"visible punctuation", "gateway/=+.:_-!~", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			backing := strings.Repeat("p", 4096) + test.id + strings.Repeat("s", 4096)
			input := backing[4096 : 4096+len(test.id)]
			ctx := withGatewayRequestID(withInvocationID(context.Background(), input), input)
			want := ""
			if test.valid {
				want = input
			}
			if InvocationID(ctx) != want || GatewayRequestID(ctx) != want {
				t.Fatalf("unexpected correlation visibility: %q %q", InvocationID(ctx), GatewayRequestID(ctx))
			}
			if test.valid && (unsafe.StringData(InvocationID(ctx)) == unsafe.StringData(input) || unsafe.StringData(GatewayRequestID(ctx)) == unsafe.StringData(input)) {
				t.Fatal("bounded metadata retained the source backing")
			}
		})
	}
}

func TestInvocationHTTPRequestReplacesGatewayOnly(t *testing.T) {
	key := struct{}{}
	parent, cancel := context.WithDeadline(context.WithValue(context.Background(), key, "preserved"), time.Now().Add(time.Minute))
	defer cancel()
	parent = withGatewayRequestID(withInvocationID(parent, "runtime-parent"), "previous-gateway")
	for _, id := range []string{"gateway/=+", "", strings.Repeat("x", 257), "café"} {
		e := event()
		e.RequestContext.RequestID = id
		e.Headers["lambda-runtime-aws-request-id"] = "spoof-runtime"
		e.Headers["x-request-id"] = "spoof-gateway"
		e.Headers["traceparent"] = "spoof-trace"
		req, err := e.HTTPRequest(parent)
		if err != nil {
			t.Fatal(err)
		}
		want := id
		if id == "" || len(id) > 256 || id == "café" {
			want = ""
		}
		if InvocationID(req.Context()) != "runtime-parent" || GatewayRequestID(req.Context()) != want {
			t.Fatal("conversion confused runtime and gateway identity")
		}
		if req.Context().Value(key) != "preserved" || req.URL.Query().Get("plus") != "+" || req.Method != "POST" || req.ContentLength != 0 {
			t.Fatal("conversion changed unrelated request state")
		}
		before, _ := parent.Deadline()
		after, _ := req.Context().Deadline()
		if !before.Equal(after) {
			t.Fatal("deadline changed")
		}
		req.Body.Close()
	}
	if GatewayRequestID(parent) != "previous-gateway" {
		t.Fatal("conversion mutated parent metadata")
	}
	cancel()
	e := event()
	e.RequestContext.RequestID = "canceled-gateway"
	req, err := e.HTTPRequest(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer req.Body.Close()
	if !errors.Is(req.Context().Err(), context.Canceled) || InvocationID(req.Context()) != "runtime-parent" || GatewayRequestID(req.Context()) != "canceled-gateway" {
		t.Fatal("cancellation lost context values")
	}
	if _, err := e.HTTPRequest(nil); err == nil {
		t.Fatal("nil request context no longer returns its existing construction error")
	}
}

func TestInvocationAdaptExposesOnlyEventGateway(t *testing.T) {
	e := event()
	e.RequestContext.RequestID = "gateway-direct"
	e.Headers["lambda-runtime-aws-request-id"] = "spoofed-runtime"
	response, err := Adapt(context.Background(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if InvocationID(r.Context()) != "" || GatewayRequestID(r.Context()) != "gateway-direct" {
			t.Error("standalone conversion invented runtime metadata")
		}
		_, _ = w.Write([]byte("ok"))
	}), e)
	if err != nil || response.StatusCode != 200 || response.Body != "ok" {
		t.Fatal(response, err)
	}
	for key := range response.Headers {
		if key != "content-type" {
			t.Fatalf("metadata published an automatic header: %s", key)
		}
	}
}
