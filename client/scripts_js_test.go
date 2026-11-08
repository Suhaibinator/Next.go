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

func TestScriptVendorDataParticipatesInSourceAdmission(t *testing.T) {
	oldLoads, oldAliases, oldSources := scriptLoads, scriptAliases, scriptSources
	t.Cleanup(func() { scriptLoads, scriptAliases, scriptSources = oldLoads, oldAliases, oldSources })
	scriptLoads, scriptAliases, scriptSources = map[string]*scriptLoad{}, map[string]string{}, map[string]string{}
	first, _ := scripts.NewDataAttributes(map[string]string{"data-site": "first.test"})
	second, _ := scripts.NewDataAttributes(map[string]string{"data-site": "second.test"})
	s := scripts.Script{ID: "vendor", Src: "https://vendor.test/settings.js", Data: first}
	s, err := s.Check()
	if err != nil {
		t.Fatal(err)
	}
	scriptSources[s.Src] = s.Key()
	s.Data = second
	if err := checkScriptIdentity(s); err == nil {
		t.Fatal("one source accepted incompatible vendor data")
	}
}
