package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestInspectCommandOutputsMachineReadableSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/_nextgo/inspect" {
			t.Errorf("unexpected inspection request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"version":1,"revision":7,"state":"ready","buildId":"good","generation":"one","serving":true,"stale":false,"graph":{"nodes":[{"id":"n0","kind":"page","pattern":"/posts/:slug"}],"edges":[],"truncated":false}}`))
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	var output bytes.Buffer
	if err := executeInspectOutput(context.Background(), []string{"-addr", u.Host}, &output); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result["state"] != "ready" || result["buildId"] != "good" {
		t.Fatalf("unexpected output: %s %v", output.String(), err)
	}
}

func TestInspectCommandRejectsUnsupportedArguments(t *testing.T) {
	for _, args := range [][]string{{"app"}, {"-addr", "example.com:3000"}, {"-addr", "localhost:0"}, {"-unknown"}} {
		var output bytes.Buffer
		if err := executeInspectOutput(context.Background(), args, &output); err == nil || output.Len() != 0 {
			t.Fatalf("accepted arguments %v, output %s, error %v", args, output.String(), err)
		}
	}
	// This verifies the public command dispatch, including the command-specific
	// address policy, without creating or compiling an application.
	err := execute(context.Background(), []string{"inspect", "-addr", "192.0.2.1:3000"})
	if err == nil || !strings.Contains(err.Error(), "loopback IP") {
		t.Fatalf("inspect was not dispatched: %v", err)
	}
}
