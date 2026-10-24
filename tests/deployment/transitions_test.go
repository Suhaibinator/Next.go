//go:build deployment

package deployment

import (
	"bytes"
	"net/http"
	"testing"
	"time"
)

func TestTransitionPolicyHTTPExecutable(t *testing.T) {
	addr := address(t)
	p := start(t, []string{"NEXTGO_DEMO_VIEW_TRANSITIONS=true"}, "-addr", addr)
	client := &http.Client{Timeout: 3 * time.Second}
	for until := time.Now().Add(5 * time.Second); ; {
		response, err := client.Get("http://" + addr + "/transitions")
		if err == nil {
			data := body(t, response)
			if response.StatusCode != 200 || !bytes.Contains(data, []byte(`data-nextgo-transitions="true"`)) || !bytes.Contains(data, []byte("Go view transitions")) || !bytes.Contains(data, []byte(`data-transition-count`)) {
				t.Fatal("transition SSR policy not embedded", response.StatusCode)
			}
			break
		}
		if !time.Now().Before(until) {
			t.Fatal("transition executable did not start", err, p.logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	p.stop(t)
}

func TestTransitionPolicyLambdaExecutable(t *testing.T) {
	lambdaExecutableRuntimePolicy(t, "/animated", false, "", false, true)
}
