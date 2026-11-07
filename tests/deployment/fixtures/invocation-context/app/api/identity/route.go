//go:build !js

// Package identity is a test-only generated application endpoint. Never expose
// these correlation diagnostics as a production application API.
package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/serverless"
)

type observation struct {
	Sequence           int    `json:"sequence"`
	Invocation         string `json:"invocation"`
	Gateway            string `json:"gateway"`
	RetainedInvocation string `json:"retainedInvocation"`
	RetainedGateway    string `json:"retainedGateway"`
}

var retained struct {
	sync.Mutex
	first context.Context
}

func capture(ctx context.Context, sequence int) observation {
	retained.Lock()
	if retained.first == nil {
		retained.first = ctx
	}
	first := retained.first
	retained.Unlock()
	return observation{Sequence: sequence, Invocation: serverless.InvocationID(ctx), Gateway: serverless.GatewayRequestID(ctx), RetainedInvocation: serverless.InvocationID(first), RetainedGateway: serverless.GatewayRequestID(first)}
}

func callbackURL() (string, error) {
	text := os.Getenv("NEXTGO_TEST_INVOCATION_CALLBACK_URL")
	u, err := url.Parse(text)
	if err != nil || len(text) > 512 || u.Scheme != "http" || u.User != nil || u.Path != "/after" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(text, "\r\n\t\\") {
		return "", errors.New("invalid fixture callback endpoint")
	}
	ip := net.ParseIP(u.Hostname())
	port, portErr := strconv.Atoi(u.Port())
	if ip == nil || !ip.IsLoopback() || portErr != nil || port < 1 || port > 65535 || strconv.Itoa(port) != u.Port() {
		return "", errors.New("fixture callback must be explicit loopback")
	}
	return text, nil
}

func Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method", 405)
		return
	}
	sequence, err := strconv.Atoi(r.URL.Query().Get("sequence"))
	endpoint, endpointErr := callbackURL()
	if err != nil || sequence < 1 || sequence > 3 || endpointErr != nil {
		http.Error(w, "invalid test fixture request", 400)
		return
	}
	value := capture(r.Context(), sequence)
	if err := nextgo.After(r.Context(), func(work context.Context) error {
		data, err := json.Marshal(capture(work, sequence))
		if err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(work, "POST", endpoint, bytes.NewReader(data))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
		if err != nil || response.StatusCode != 204 {
			return errors.New("fixture callback was not accepted")
		}
		return nil
	}); err != nil {
		http.Error(w, "callback unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
