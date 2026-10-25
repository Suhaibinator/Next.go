//go:build js && wasm

package client

import (
	"encoding/json"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/offlinejson"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/offline"
)

var deployment paths.Config

// URL maps an application-absolute path to its browser URL. Navigate, Replace
// and Prefetch accept browser URLs; use this helper for app-relative Go inputs.
func URL(local string) (string, error) { return deployment.URL(local) }
func MustURL(local string) string {
	value, err := URL(local)
	if err != nil {
		panic(err)
	}
	return value
}

// AssetURL maps public files into this document's asset generation. Validated
// saved documents use immutable captured public assets, including in lazy Go
// components. Unknown dynamic files still require the network; this helper
// cannot add them to the approved saved inventory.
func AssetURL(local string) (string, error) {
	configured, err := assetDeployment()
	if err != nil {
		return "", err
	}
	return configured.AssetURL(local)
}

func assetDeployment() (configured paths.Config, err error) {
	configured = deployment
	defer func() {
		if recover() != nil {
			err = ErrOfflineProtocol
		}
	}()
	if doc.Type() != js.TypeObject || doc.IsNull() {
		return configured, nil
	}
	body := doc.Get("body")
	if body.Type() != js.TypeObject || body.IsNull() || body.Get("getAttribute").Type() != js.TypeFunction {
		return configured, nil
	}
	marker := body.Call("getAttribute", "data-nextgo-offline-saved")
	if marker.IsNull() {
		return configured, nil
	}
	if marker.Type() != js.TypeString || marker.String() != "1" {
		return configured, ErrOfflineProtocol
	}
	raw := body.Call("getAttribute", offline.ClientDescriptorAttribute)
	if raw.Type() != js.TypeString || js.Global().Get("Object").Invoke(raw).Get("length").Int() > offline.MaxClientDescriptorBytes {
		return configured, ErrOfflineProtocol
	}
	encoded := raw.String()
	var descriptor offline.ClientDescriptor
	if len(encoded) > offline.MaxClientDescriptorBytes || offlinejson.Decode([]byte(encoded), &descriptor) != nil {
		return configured, ErrOfflineProtocol
	}
	build := body.Call("getAttribute", "data-nextgo-build").String()
	configuration := documentConfiguration()
	// Recompute the normal document identity instead of trusting fields inside
	// the saved descriptor to authorize their own runtime or deployment paths.
	var manifest navigation.Manifest
	if rawManifest := body.Call("getAttribute", "data-nextgo-navigation"); !rawManifest.IsNull() {
		if rawManifest.Type() != js.TypeString || json.Unmarshal([]byte(rawManifest.String()), &manifest) != nil {
			return configured, ErrOfflineProtocol
		}
	}
	if configuration != navigation.ConfigurationID(build, configured, manifest, body.Call("getAttribute", "data-nextgo-preserve-layouts").String() == "true", body.Call("getAttribute", "data-nextgo-transitions").String() == "true") {
		return configured, ErrOfflineProtocol
	}
	if moduleMode {
		if !moduleHost.Truthy() || moduleHost.Get("version").Type() != js.TypeNumber || moduleHost.Get("version").Int() != moduleABI || moduleHost.Get("state").Type() != js.TypeFunction {
			return configured, ErrOfflineProtocol
		}
		var state moduleState
		if json.Unmarshal([]byte(moduleHost.Call("state").String()), &state) != nil || state.Configuration != configuration || state.Paths != configured {
			return configured, ErrOfflineProtocol
		}
	}
	if offline.ValidateClientDescriptor(descriptor, win.Get("location").Get("origin").String(), configured.BasePath, build, configuration) != nil {
		return configured, ErrOfflineProtocol
	}
	configured.AssetPrefix = configured.BasePath + "/_nextgo/" + build + "/offline-public"
	return configured, nil
}
func MustAssetURL(local string) string {
	value, err := AssetURL(local)
	if err != nil {
		panic(err)
	}
	return value
}
