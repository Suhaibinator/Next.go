//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"github.com/Suhaibinator/Next.go/scripts"
	"strings"
	"testing"
)

func TestScriptDependencyAdmissionAndCanceledCaller(t *testing.T) {
	oldLoads, oldAliases, oldSources, oldURL, oldWaiters := scriptLoads, scriptAliases, scriptSources, currentURL, scriptWaiters
	t.Cleanup(func() {
		scriptLoads, scriptAliases, scriptSources, currentURL, scriptWaiters = oldLoads, oldAliases, oldSources, oldURL, oldWaiters
	})
	scriptLoads = map[string]*scriptLoad{}
	scriptAliases = map[string]string{}
	scriptSources = map[string]string{}
	currentURL = "https://site.test/"
	scriptWaiters = 0
	s := scripts.Script{ID: "one", Src: "https://cdn.test/lib.js"}
	s, _ = s.Check()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := LoadScript(ctx, s); err != context.Canceled || len(scriptLoads) != 0 {
		t.Fatal("canceled caller registered work", err)
	}
	scriptWaiters = maxScriptWaiters
	if err := LoadScript(context.Background(), s); err == nil || !strings.Contains(err.Error(), "waiter limit") {
		t.Fatal("waiter admission ignored", err)
	}
	scriptAliases[s.ID] = "another source"
	if err := checkScriptIdentity(s); err == nil {
		t.Fatal("ID changed its loaded dependency")
	}
	scriptAliases = map[string]string{}
	scriptSources[s.Src] = "another security policy"
	if err := checkScriptIdentity(s); err == nil {
		t.Fatal("source changed its security policy")
	}
}
