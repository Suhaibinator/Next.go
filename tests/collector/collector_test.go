//go:build collector

package collector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type output struct {
	mu sync.Mutex
	bytes.Buffer
}

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := len(p)
	if o.Len() < 1<<20 {
		_, _ = o.Buffer.Write(p[:min(n, (1<<20)-o.Len())])
	}
	return n, nil
}
func (o *output) text() string { o.mu.Lock(); defer o.mu.Unlock(); return o.Buffer.String() }
func address(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}
func launch(t *testing.T, binary string, env, args []string) (*exec.Cmd, <-chan error, *output) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Dir = "/"
	logs := &output{}
	cmd.Stdout, cmd.Stderr = logs, logs
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd, done, logs
}
func stop(t *testing.T, cmd *exec.Cmd, done <-chan error, logs *output) {
	t.Helper()
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err, logs.text())
		}
	case <-time.After(8 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("process did not drain", logs.text())
	}
}

func TestGeneratedApplicationExportsToOfficialCollector(t *testing.T) {
	collector := os.Getenv("NEXTGO_COLLECTOR_BINARY")
	server := os.Getenv("NEXTGO_TEST_BINARY")
	if collector == "" || server == "" {
		t.Skip("set collector and generated application binaries")
	}
	directory := t.TempDir()
	collectorAddr := address(t)
	serverAddr := address(t)
	config := filepath.Join(directory, "collector.yaml")
	result := filepath.Join(directory, "traces.json")
	source := fmt.Sprintf("receivers:\n  otlp:\n    protocols:\n      http:\n        endpoint: %s\nexporters:\n  file:\n    path: %s\nservice:\n  telemetry:\n    logs:\n      level: error\n  pipelines:\n    traces:\n      receivers: [otlp]\n      exporters: [file]\n", collectorAddr, result)
	if err := os.WriteFile(config, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	c, cDone, cLogs := launch(t, collector, nil, []string{"--config=file:" + config})
	client := &http.Client{Timeout: 2 * time.Second}
	ready := false
	for until := time.Now().Add(10 * time.Second); time.Now().Before(until); {
		request, _ := http.NewRequest("POST", "http://"+collectorAddr+"/v1/traces", strings.NewReader(`{"resourceSpans":[]}`))
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !ready {
		t.Fatal("collector not ready", cLogs.text())
	}
	app, appDone, appLogs := launch(t, server, []string{"NEXTGO_DEMO_OTLP_ENDPOINT=http://" + collectorAddr + "/v1/traces"}, []string{"-addr", serverAddr})
	ready = false
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
		response, err := client.Get("http://" + serverAddr + "/api/health")
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !ready {
		t.Fatal("application not ready", appLogs.text())
	}
	for _, path := range []string{"/posts/html-first?secret=private-collector-probe", "/lifecycle"} {
		request, _ := http.NewRequest("GET", "http://"+serverAddr+path, nil)
		request.Header.Set("Authorization", "Bearer private-collector-probe")
		request.Header.Set("Cookie", "session=private-collector-probe")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal(path, response.StatusCode)
		}
	}
	stop(t, app, appDone, appLogs)
	stop(t, c, cDone, cLogs)
	data, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err, cLogs.text())
	}
	if bytes.Contains(data, []byte("private-collector-probe")) {
		t.Fatal("sensitive values reached official collector")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	names := map[string]bool{}
	service := false
	count := 0
	for {
		var request map[string]any
		if err := decoder.Decode(&request); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err, string(data))
		}
		resources, _ := request["resourceSpans"].([]any)
		for _, raw := range resources {
			resource := raw.(map[string]any)
			attributes, _ := resource["resource"].(map[string]any)["attributes"].([]any)
			for _, raw := range attributes {
				a := raw.(map[string]any)
				if a["key"] == "service.name" && a["value"].(map[string]any)["stringValue"] == "nextgo-demo" {
					service = true
				}
			}
			for _, raw := range resource["scopeSpans"].([]any) {
				for _, raw := range raw.(map[string]any)["spans"].([]any) {
					span := raw.(map[string]any)
					names[span["name"].(string)] = true
					count++
					if len(span["traceId"].(string)) != 32 || len(span["spanId"].(string)) != 16 {
						t.Fatal(span)
					}
				}
			}
		}
	}
	if !service || !names["nextgo.request"] || !names["nextgo.api"] || !names["nextgo.load"] || !names["nextgo.after"] {
		t.Fatal(service, names, string(data), appLogs.text())
	}
	t.Logf("official collector accepted %d spans with service ownership and completion drain", count)
}
