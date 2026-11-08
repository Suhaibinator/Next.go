//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall/js"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/scripts"
)

func importEnvironment(t *testing.T) js.Value {
	t.Helper()
	oldWin, oldDoc, oldURL := win, doc, currentURL
	oldImports, oldAliases, oldWaiters, oldHostWaiters, oldSources := importedModules, importAliases, importWaiters, importHostWaiters, scriptSources
	f := js.Global().Get("Function").New(`
const pending=new Map(),calls=[];
const w={Object,Promise,URL,AbortController,__nextgoImportModule(src){calls.push(src);return new Promise((resolve,reject)=>pending.set(src,{resolve,reject}));}};
return {win:w,calls,resolve(src){pending.get(src).resolve({default:{version:'v1'},named:42});},reject(src){pending.get(src).reject(new Error('fixture import failed'));}};
`).Invoke()
	win, doc, currentURL = f.Get("win"), js.ValueOf(map[string]any{"baseURI": "https://site.test/"}), "https://site.test/"
	importedModules, importAliases, importWaiters, scriptSources = nil, nil, 0, map[string]string{}
	importHostWaiters = 0
	t.Cleanup(func() {
		win, doc, currentURL = oldWin, oldDoc, oldURL
		importedModules, importAliases, importWaiters, scriptSources = oldImports, oldAliases, oldWaiters, oldSources
		importHostWaiters = oldHostWaiters
	})
	return f
}

func importUntil(t *testing.T, check func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); !check(); {
		if time.Now().After(deadline) {
			t.Fatal("import condition did not settle")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestImportModuleHostBoundsPendingRequestsBeforeScheduling(t *testing.T) {
	f := importEnvironment(t)
	const source = "https://modules.test/host.mjs"
	args := []js.Value{js.ValueOf(`{"id":"host","src":"` + source + `"}`)}
	promises := make([]js.Value, maxImportWaiters)
	for i := range promises {
		promises[i] = importModuleHostPromise(args).(js.Value)
	}
	if importHostWaiters != maxImportWaiters {
		t.Fatal("host capacity not reserved synchronously", importHostWaiters)
	}
	controller := win.Get("AbortController").New()
	overflow := importModuleHostPromise(args).(js.Value)
	if _, err := awaitBrowserPromise(context.Background(), overflow, controller); err == nil || !strings.Contains(err.Error(), "host waiter limit") {
		t.Fatal("unbounded pending host requests", err)
	}
	importUntil(t, func() bool { return f.Get("calls").Length() == 1 })
	f.Call("resolve", source)
	for _, promise := range promises {
		holder, err := awaitBrowserPromise(context.Background(), promise, controller)
		if err != nil || holder.Get("namespace").Get("named").Int() != 42 {
			t.Fatal("host lost shared export namespace", err)
		}
	}
	importUntil(t, func() bool { return importHostWaiters == 0 && importWaiters == 0 })
}

func TestImportModuleSharesOnePhysicalImportAfterConsumerCancellation(t *testing.T) {
	f := importEnvironment(t)
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	second := make(chan promiseResult, 1)
	const source = "https://modules.test/v1.mjs?lang=fr&lang=en"
	go func() { _, err := ImportModule(ctx, scripts.Module{ID: "first", Src: source}); first <- err }()
	go func() {
		value, err := ImportModule(context.Background(), scripts.Module{ID: "second", Src: source})
		second <- promiseResult{value, err}
	}()
	importUntil(t, func() bool { return importWaiters == 2 && f.Get("calls").Length() == 1 })
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if importWaiters != 1 || len(importedModules) != 1 || f.Get("calls").Index(0).String() != source {
		t.Fatal("consumer cancellation discarded shared work or changed query identity")
	}
	f.Call("resolve", source)
	result := <-second
	if result.err != nil || result.value.Get("named").Int() != 42 || result.value.Get("default").Get("version").String() != "v1" {
		t.Fatal("namespace exports not returned", result.err)
	}
	value, err := ImportModule(context.Background(), scripts.Module{ID: "first", Src: source})
	if err != nil || !value.Equal(result.value) || f.Get("calls").Length() != 1 || importWaiters != 0 {
		t.Fatal("cached namespace changed or reexecuted", err)
	}
	if _, err := ImportModule(context.Background(), scripts.Module{ID: "first", Src: "https://modules.test/other.mjs"}); err == nil {
		t.Fatal("identity rebound to another module")
	}
	if err := checkScriptIdentity(scripts.Script{ID: "script", Src: source, Type: "module"}); err == nil {
		t.Fatal("script loader silently changed imported-module policy")
	}
}

func TestImportModuleFailuresStayReservedAndRejectScriptPolicyReuse(t *testing.T) {
	f := importEnvironment(t)
	const source = "https://modules.test/fails.mjs"
	done := make(chan error, 1)
	go func() {
		_, err := ImportModule(context.Background(), scripts.Module{ID: "fails", Src: source})
		done <- err
	}()
	importUntil(t, func() bool { return f.Get("calls").Length() == 1 })
	f.Call("reject", source)
	if err := <-done; err == nil {
		t.Fatal("failed import succeeded")
	}
	if _, err := ImportModule(context.Background(), scripts.Module{ID: "alias", Src: source}); err == nil || f.Get("calls").Length() != 1 {
		t.Fatal("failed URL retried implicitly", err)
	}
	scriptSources["https://modules.test/script.mjs"] = "existing-script-policy"
	if _, err := ImportModule(context.Background(), scripts.Module{ID: "script", Src: "https://modules.test/script.mjs"}); err == nil {
		t.Fatal("import ignored an existing script policy")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ImportModule(ctx, scripts.Module{ID: "canceled", Src: "https://modules.test/new.mjs"}); !errors.Is(err, context.Canceled) || f.Get("calls").Length() != 1 {
		t.Fatal("canceled call registered new work", err)
	}
}

func TestImportModuleRootAndAliasBoundsDoNotReleaseOnCancellation(t *testing.T) {
	f := importEnvironment(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, maxImportedModules)
	for i := 0; i < maxImportedModules; i++ {
		input := scripts.Module{ID: fmt.Sprintf("module-%d", i), Src: fmt.Sprintf("https://modules.test/v%d.mjs", i)}
		go func() { _, err := ImportModule(ctx, input); done <- err }()
	}
	importUntil(t, func() bool {
		return len(importedModules) == maxImportedModules && f.Get("calls").Length() == maxImportedModules
	})
	cancel()
	for i := 0; i < maxImportedModules; i++ {
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if _, err := ImportModule(context.Background(), scripts.Module{ID: "over", Src: "https://modules.test/over.mjs"}); err == nil || !strings.Contains(err.Error(), "root-module limit") {
		t.Fatal("cancellation manufactured physical root capacity", err)
	}
	for source, state := range importedModules {
		f.Call("resolve", source)
		awaitWidget(t, state.done)
	}
	for i := maxImportedModules; i < maxImportAliases; i++ {
		if _, err := ImportModule(context.Background(), scripts.Module{ID: fmt.Sprintf("alias-%d", i), Src: "https://modules.test/v0.mjs"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ImportModule(context.Background(), scripts.Module{ID: "too-many-aliases", Src: "https://modules.test/v0.mjs"}); err == nil || !strings.Contains(err.Error(), "alias limit") {
		t.Fatal("alias limit ignored", err)
	}
}

func TestImportModuleWaiterBoundReleasesConsumersWithoutDuplicatingPhysicalCallbacks(t *testing.T) {
	f := importEnvironment(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, maxImportWaiters)
	input := scripts.Module{ID: "shared", Src: "https://modules.test/shared.mjs"}
	for i := 0; i < maxImportWaiters; i++ {
		go func() { _, err := ImportModule(ctx, input); done <- err }()
	}
	importUntil(t, func() bool { return importWaiters == maxImportWaiters })
	if _, err := ImportModule(context.Background(), input); err == nil || !strings.Contains(err.Error(), "waiter limit") {
		t.Fatal("waiter capacity ignored", err)
	}
	cancel()
	for i := 0; i < maxImportWaiters; i++ {
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if importWaiters != 0 || f.Get("calls").Length() != 1 {
		t.Fatal("canceled waiters leaked or duplicated work")
	}
	f.Call("resolve", input.Src)
	awaitWidget(t, importedModules[input.Src].done)
}
