//go:build js && wasm

package client

import (
	"encoding/json"
	"errors"
	"strings"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/offline"
)

type savedAssetFixture struct {
	attrs             map[string]string
	descriptor        offline.ClientDescriptor
	hostConfiguration string
	hostPaths         paths.Config
	stateCalls        int
}

func savedAssetEnvironment(t *testing.T, base string) *savedAssetFixture {
	t.Helper()
	oldDoc, oldWin, oldPaths, oldHost := doc, win, deployment, moduleHost
	var functions []js.Func
	t.Cleanup(func() {
		doc, win, deployment, moduleHost = oldDoc, oldWin, oldPaths, oldHost
		for _, fn := range functions {
			fn.Release()
		}
	})
	fn := func(callback func(js.Value, []js.Value) any) js.Func {
		value := js.FuncOf(callback)
		functions = append(functions, value)
		return value
	}
	deployment = paths.Config{BasePath: base, AssetPrefix: "https://assets.test"}
	manifest := navigation.Manifest{Routes: []navigation.Route{{Pattern: "/"}, {Pattern: "/guide"}}}
	build := strings.Repeat("a", 20)
	configuration := navigation.ConfigurationID(build, deployment, manifest, false, false)
	scope := "https://reader.test" + base
	if base == "" {
		scope += "/"
	}
	f := &savedAssetFixture{attrs: map[string]string{"data-nextgo-offline-saved": "1", "data-nextgo-build": build, "data-nextgo-configuration": configuration}, hostConfiguration: configuration, hostPaths: deployment}
	f.descriptor = offline.ClientDescriptor{Version: 1, Scope: scope, WorkerURL: "https://reader.test" + base + offline.WorkerScriptPath, CatalogURL: "https://reader.test" + base + offline.CatalogPath, OwnerPolicyID: "reader", RuntimeID: build, ConfigurationID: configuration}
	raw, _ := json.Marshal(f.descriptor)
	f.attrs[offline.ClientDescriptorAttribute] = string(raw)
	raw, _ = json.Marshal(manifest)
	f.attrs["data-nextgo-navigation"] = string(raw)
	doc = js.ValueOf(map[string]any{"body": map[string]any{"getAttribute": fn(func(_ js.Value, args []js.Value) any {
		if value, ok := f.attrs[args[0].String()]; ok {
			return value
		}
		return js.Null()
	})}})
	win = js.ValueOf(map[string]any{"location": map[string]any{"origin": "https://reader.test"}})
	moduleHost = js.ValueOf(map[string]any{"version": moduleABI, "state": fn(func(js.Value, []js.Value) any {
		f.stateCalls++
		raw, _ := json.Marshal(moduleState{Configuration: f.hostConfiguration, Paths: f.hostPaths})
		return string(raw)
	})})
	return f
}

func TestSavedAssetURLPreservesRuntimeGeometryAndNormalFastPath(t *testing.T) {
	for _, base := range []string{"", "/reader"} {
		t.Run(base, func(t *testing.T) {
			f := savedAssetEnvironment(t, base)
			original := deployment
			runtime := deployment.RuntimeURL(strings.Repeat("a", 20))
			got, err := AssetURL("/images/cover.svg?v=one#icon")
			want := base + "/_nextgo/" + strings.Repeat("a", 20) + "/offline-public/assets/images/cover.svg?v=one#icon"
			if err != nil || got != want {
				t.Fatalf("saved public file escaped captured generation: %q %v", got, err)
			}
			if deployment != original || deployment.RuntimeURL(strings.Repeat("a", 20)) != runtime {
				t.Fatal("saved file mapping changed lazy/runtime deployment")
			}
			if moduleMode && f.stateCalls != 1 {
				t.Fatal("module did not bind the core-owned configuration")
			}
			delete(f.attrs, "data-nextgo-offline-saved")
			f.stateCalls = 0
			got, err = AssetURL("/images/cover.svg")
			if err != nil || got != "https://assets.test/assets/images/cover.svg" || f.stateCalls != 0 {
				t.Fatal("ordinary assets changed or consulted the module bridge", got, err)
			}
			doc = js.Undefined()
			if got, err = AssetURL("/before-runtime.svg"); err != nil || got != "https://assets.test/assets/before-runtime.svg" {
				t.Fatal("ordinary pre-runtime helper behavior changed", got, err)
			}
		})
	}
}

func TestSavedAssetURLRejectsSelfAuthorizedDescriptorOrModuleIdentity(t *testing.T) {
	scenarios := []string{"marker", "descriptor-runtime", "descriptor-configuration", "foreign-worker", "mounted-scope", "body-runtime"}
	if moduleMode {
		scenarios = append(scenarios, "host-configuration", "host-paths")
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			f := savedAssetEnvironment(t, "/reader")
			switch scenario {
			case "marker":
				f.attrs["data-nextgo-offline-saved"] = "true"
			case "descriptor-runtime":
				f.descriptor.RuntimeID = strings.Repeat("c", 20)
			case "descriptor-configuration":
				f.descriptor.ConfigurationID = strings.Repeat("c", 64)
			case "foreign-worker":
				f.descriptor.WorkerURL = "https://foreign.test/sw.js"
			case "mounted-scope":
				f.descriptor.Scope += "/"
			case "body-runtime":
				f.attrs["data-nextgo-build"] = strings.Repeat("c", 20)
				f.descriptor.RuntimeID = strings.Repeat("c", 20)
			case "host-configuration":
				f.hostConfiguration = strings.Repeat("c", 64)
			case "host-paths":
				f.hostPaths.BasePath = "/other"
			}
			raw, _ := json.Marshal(f.descriptor)
			f.attrs[offline.ClientDescriptorAttribute] = string(raw)
			if got, err := AssetURL("/cover.svg"); !errors.Is(err, ErrOfflineProtocol) || got != "" {
				t.Fatal("saved asset accepted unbound document ownership", got, err)
			}
		})
	}
}
