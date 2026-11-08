//go:build js && wasm

package client

import (
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"syscall/js"
)

func rootBoundaryURL(u js.Value) bool {
	if currentURL == "" || u.Type() != js.TypeObject || !eligibleDocumentURL(u) || !navigation.ValidConfigurationID(documentRoot()) {
		return false
	}
	path, _ := deployment.Strip(u.Get("pathname").String())
	root := routeManifest.RootForPath(path)
	return navigation.ValidConfigurationID(root) && root != documentRoot()
}

// Root ownership stays fixed for the lifetime of this document, including
// lazy modules. It is never updated by committing a navigation response.
func documentRoot() string {
	if !doc.Truthy() {
		return ""
	}
	body := doc.Get("body")
	if !body.Truthy() || body.Get("getAttribute").Type() != js.TypeFunction {
		return ""
	}
	value := body.Call("getAttribute", "data-nextgo-root")
	if value.IsNull() || value.IsUndefined() {
		return ""
	}
	return value.String()
}
