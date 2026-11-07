//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"fmt"
	"strings"
	"syscall/js"
	"time"

	"github.com/Suhaibinator/Next.go/internal/mediatype"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
)

const staticShellAttribute = "data-nextgo-static-shell"

var staticShell *navigation.StaticShellDescriptor

func init() {
	registeredStaticShellInitializer = initializeStaticShellDocument
	registeredStaticShellFetch = fetchStaticShellEnvelope
}

func initializeStaticShellDocument() error {
	staticShell = nil
	body := doc.Get("body")
	raw := body.Call("getAttribute", staticShellAttribute)
	if raw.IsNull() {
		return nil
	}
	if !staticExport() {
		return fmt.Errorf("descriptor requires a static export")
	}
	// Bound the JS string before copying it into Go, then apply the stricter
	// UTF-8 JSON byte cap and exact scalar/identity checks in the shared codec.
	if raw.Type() != js.TypeString || win.Get("Object").Invoke(raw).Get("length").Int() > navigation.MaxStaticShellDescriptorBytes {
		return fmt.Errorf("descriptor exceeds its byte limit")
	}
	descriptor, err := navigation.DecodeStaticShellDescriptor([]byte(raw.String()), deployment, body.Call("getAttribute", "data-nextgo-build").String(), documentConfiguration())
	if err != nil {
		return err
	}
	if routeManifest.Draft || routeManifest.Locale != "" || routeManifest.Parallel != "" || body.Call("getAttribute", "data-nextgo-preserve-layouts").String() == "true" {
		return fmt.Errorf("unsupported static shell document policy")
	}
	for _, marker := range []string{"data-nextgo-offline", "data-nextgo-offline-saved", "data-nextgo-draft", "data-nextgo-metadata-version", "data-nextgo-metadata-owner"} {
		if body.Call("hasAttribute", marker).Bool() {
			return fmt.Errorf("unsupported static shell document marker")
		}
	}
	// An exporter declares one actual page route. Do not borrow this shell if
	// a stale manifest omitted it, or a more specific page/endpoint wins.
	if _, ok := descriptor.MatchTarget(descriptor.SeedURL, deployment, routeManifest); !ok {
		return fmt.Errorf("static shell seed does not select its declared page")
	}
	app := doc.Call("getElementById", "nextgo-app")
	if app.IsNull() || app.IsUndefined() {
		return fmt.Errorf("missing static shell app root")
	}
	u := win.Get("URL").New(win.Get("location").Get("href"))
	if _, ok := descriptor.MatchTarget(u.Get("pathname").String(), deployment, routeManifest); ok {
		if !eligibleURL(u) {
			return fmt.Errorf("static shell target is outside application ownership")
		}
		if err := validateStaticShellTree(app, descriptor.Component); err != nil {
			return err
		}
		if err := validateStaticShellInitialHead(); err != nil {
			return err
		}
	} else {
		islands := app.Call("querySelectorAll", "[data-nextgo-island]")
		for i := 0; i < islands.Length(); i++ {
			if islands.Index(i).Call("getAttribute", "data-nextgo-island").String() == descriptor.Component {
				return fmt.Errorf("static shell document does not own the visible target")
			}
		}
	}
	staticShell = &descriptor
	return nil
}

// A positive match is the only way to select this immutable asset. Ordinary
// static routes retain their exact JSON requests and ordinary missing JSON can
// never turn into shell success. The caller owns cancellation and commit epochs.
func fetchStaticShellEnvelope(ctx context.Context, target string) (data envelope, ttl time.Duration, handled, fallback bool, err error) {
	if staticShell == nil {
		return data, 0, false, false, nil
	}
	descriptor := *staticShell
	u := win.Get("URL").New(target)
	if _, ok := descriptor.MatchTarget(u.Get("pathname").String(), deployment, routeManifest); !ok {
		return data, 0, false, false, nil
	}
	if !eligibleURL(u) {
		return data, 0, true, false, fmt.Errorf("ineligible static shell target")
	}
	if err := ctx.Err(); err != nil {
		return data, 0, true, false, err
	}
	local, _ := deployment.Strip(u.Get("pathname").String())
	canonicalPath, pathErr := deployment.URL(paths.Logical(local))
	if pathErr != nil {
		return data, 0, true, false, pathErr
	}
	u.Set("pathname", canonicalPath)
	validatedTarget := u.Get("href").String()
	fetchURL := win.Get("URL").New(descriptor.EnvelopeURL, win.Get("location").Get("href"))
	started := time.Now()
	response, fetchErr := Fetch(ctx, fetchURL.Get("href").String(), FetchOptions{Headers: map[string]string{"Cache-Control": "no-cache"}, Cache: "no-store", Redirect: "error", MaxBytes: int64(descriptor.Bytes)})
	if fetchErr != nil {
		return data, 0, true, false, fetchErr
	}
	if err := ctx.Err(); err != nil {
		return data, 0, true, false, err
	}
	mediaType, _, mediaErr := mediatype.ParseMediaType(response.Header.Get("Content-Type"))
	if response.URL != fetchURL.Get("href").String() || !sameOriginResponse(response.URL) || response.StatusCode != 200 || mediaErr != nil || mediaType != "application/json" {
		return data, 0, true, false, fmt.Errorf("invalid immutable static shell response")
	}
	// Verify the exact stored bytes before any JSON decoding or DOM parsing.
	if err := descriptor.CheckEnvelope(response.Body); err != nil {
		return data, 0, true, false, err
	}
	data, ttl, fallback, err = decodeEnvelopeResponse(response, response.Body, validatedTarget, started, false)
	if err != nil {
		return data, 0, true, false, err
	}
	if fallback {
		// An immutable publication failure is terminal for this navigation.
		// Preserve the current view rather than reloading the same bad asset.
		return envelope{}, 0, true, false, fmt.Errorf("incompatible immutable static shell envelope")
	}
	if err := validateStaticShellSeed(data, descriptor); err != nil {
		return envelope{}, 0, true, false, err
	}
	if err := ctx.Err(); err != nil {
		return envelope{}, 0, true, false, err
	}
	// envelope is a value: only this local copy is rebound. Its fetched bytes,
	// seed props/head and selection provenance are never changed or invented.
	data.URL = validatedTarget
	return data, ttl, true, false, nil
}

func validateStaticShellSeed(data envelope, descriptor navigation.StaticShellDescriptor) error {
	if data.Version != navigation.ProtocolVersion || data.Status != 200 || data.URL != descriptor.SeedURL || data.Build != descriptor.Build || data.Configuration != descriptor.Configuration || data.Shell != "" || data.ShellRef || data.metadata != nil {
		return fmt.Errorf("invalid static shell seed envelope")
	}
	for name := range data.Meta {
		if strings.EqualFold(name, "og:url") {
			return fmt.Errorf("static shell metadata depends on the seed URL")
		}
	}
	for _, element := range data.Head {
		if err := validateStaticShellHeadElement(element.Tag, element.Attrs); err != nil {
			return err
		}
	}
	parsed := win.Get("DOMParser").New().Call("parseFromString", data.HTML, "text/html")
	if parsed.Get("head").Get("childNodes").Length() != 0 {
		return fmt.Errorf("static shell content contains document head nodes")
	}
	return validateStaticShellTree(parsed.Get("body"), descriptor.Component)
}

func validateStaticShellHeadElement(tag string, attrs map[string]string) error {
	_, hreflang := attrs["hreflang"]
	if tag == "script" || tag == "link" && (strings.EqualFold(attrs["rel"], "canonical") || strings.EqualFold(attrs["rel"], "alternate") || hreflang) || tag == "meta" && (strings.EqualFold(attrs["property"], "og:url") || strings.EqualFold(attrs["name"], "og:url")) {
		return fmt.Errorf("static shell head depends on the seed URL or structured data")
	}
	return nil
}

func validateStaticShellInitialHead() error {
	nodes := doc.Get("head").Call("querySelectorAll", "meta,link,script")
	for i := 0; i < nodes.Length(); i++ {
		node := nodes.Index(i)
		tag := node.Get("localName").String()
		if node.Call("hasAttribute", "nonce").Bool() {
			return fmt.Errorf("static shell head contains nonce-dependent output")
		}
		if tag == "script" {
			if strings.EqualFold(node.Call("getAttribute", "type").String(), "application/ld+json") || node.Call("hasAttribute", "data-nextgo-head").Bool() {
				return fmt.Errorf("static shell head contains structured data")
			}
			continue // Framework bootstrap and paired runtime glue are unchanged.
		}
		attrs := map[string]string{}
		for _, name := range []string{"rel", "hreflang", "property", "name"} {
			value := node.Call("getAttribute", name)
			if !value.IsNull() {
				attrs[name] = value.String()
			}
		}
		if err := validateStaticShellHeadElement(tag, attrs); err != nil {
			return err
		}
	}
	return nil
}

func validateStaticShellTree(root js.Value, component string) error {
	if root.IsNull() || root.IsUndefined() {
		return fmt.Errorf("missing static shell app root")
	}
	nodes := root.Call("querySelectorAll", "*")
	islands := 0
	for i := -1; i < nodes.Length(); i++ {
		node := root
		if i >= 0 {
			node = nodes.Index(i)
		}
		tag := node.Get("localName").String()
		if strings.HasPrefix(tag, "nextgo-") || tag == "template" || tag == "script" || tag == "style" || tag == "base" || tag == "link" || tag == "meta" || tag == "title" {
			return fmt.Errorf("static shell seed contains framework or document nodes")
		}
		island := node.Call("hasAttribute", "data-nextgo-island").Bool()
		if island {
			islands++
			valid, err := validateClientBoundary(node)
			if err != nil || !valid || node.Call("getAttribute", "data-nextgo-island").String() != component {
				return fmt.Errorf("static shell requires its declared ClientIsland")
			}
		}
		attrs := node.Get("attributes")
		for j := 0; j < attrs.Length(); j++ {
			name := strings.ToLower(attrs.Index(j).Get("name").String())
			if strings.HasPrefix(name, "data-nextgo-") && !(island && (name == clientBoundaryAttribute || name == "data-nextgo-island" || name == "data-nextgo-props")) {
				return fmt.Errorf("static shell seed contains unsupported framework markers")
			}
		}
	}
	if islands != 1 {
		return fmt.Errorf("static shell requires exactly one ClientIsland")
	}
	return nil
}
