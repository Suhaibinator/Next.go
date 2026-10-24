//go:build deployment

package deployment

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/telemetry"
)

func assertExecutableTraceRecords(t *testing.T, logs string, completion bool) {
	t.Helper()
	found := map[string]bool{}
	for _, line := range strings.Split(logs, "\n") {
		var record telemetry.Record
		if json.Unmarshal([]byte(line), &record) != nil || record.SpanID == "" {
			continue
		}
		if record.Name == "nextgo.request" && record.Route == "/posts/:slug" && record.Status == 200 {
			found["page"] = true
		}
		if record.Name == "nextgo.api" && record.Route == "/api/health" && record.Status == 200 {
			found["api"] = true
		}
		if record.Name == "nextgo.after" {
			found["after"] = true
		}
		data, _ := json.Marshal(record)
		if strings.Contains(string(data), "private-probe") {
			t.Fatal("sensitive request data entered trace records", string(data))
		}
	}
	if !found["page"] || !found["api"] || completion && !found["after"] {
		t.Fatal("missing executable tracing", found, logs)
	}
}

func TestTracingHTTPExecutable(t *testing.T) {
	addr := address(t)
	origin := "http://" + addr
	p := start(t, []string{"NEXTGO_DEMO_TRACING=true"}, "-addr", addr)
	client := &http.Client{Timeout: 3 * time.Second}
	ready := false
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
		response, err := client.Get(origin + "/api/health")
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
		t.Fatal("traced executable not ready", p.logs.String())
	}
	r, _ := http.NewRequest("GET", origin+"/posts/html-first?secret=private-probe-query", nil)
	r.Header.Set("Authorization", "Bearer private-probe-token")
	r.Header.Set("Cookie", "session=private-probe-cookie")
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = body(t, response)
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	p.stop(t)
	assertExecutableTraceRecords(t, p.logs.String(), false)
}

func TestTracingLambdaExecutable(t *testing.T) {
	lambdaExecutableSettings(t, "/trace", false, "", true)
}
