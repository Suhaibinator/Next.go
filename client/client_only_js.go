//go:build js && wasm

package client

import (
	"context"
	"fmt"
	"strings"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/ui"
)

const clientBoundaryAttribute = "data-nextgo-client"

type clientViewTransaction struct {
	root, view, fallback, parent js.Value
	focus                        keyedFocus
	ready                        bool
}

// ClientView replaces the fallback of an explicit ui.ClientIsland. Call it once
// during Mount and bind behavior through the returned owned child scope. The
// outer island node stays intact. A failed/panicking Mount disposes scoped work
// and restores the original fallback nodes. Arbitrary application DOM mutations
// or other side effects cannot be rolled back.
//
// The view is rendered once into a detached, validated tree (64 KiB, 2000
// descendant elements). It cannot contain nested islands, scripts, styles,
// deferred boundaries or other framework declarations. State updates afterward
// use explicit DOM/state APIs; this is not a rerendering or reconciliation API.
// Mount must return promptly, including when initializing browser-only libraries.
func (s *Scope) ClientView(node ui.Node) (*Scope, error) {
	if s == nil || !s.active() || !s.mountOpen || !s.mountClient || s.clientViewUsed {
		return nil, fmt.Errorf("nextgo: ClientView requires one call during a client island Mount")
	}
	s.clientViewUsed = true
	if node == nil || !s.Root.Value.Get("isConnected").Bool() {
		return nil, fmt.Errorf("nextgo: ClientView requires a view and a connected boundary")
	}
	if boundary, err := validateClientBoundary(s.Root.Value); err != nil || !boundary {
		return nil, fmt.Errorf("nextgo: ClientView boundary changed during Mount")
	}
	// Reuse the UI fallback renderer to reject typed islands/deferred work before
	// loaders can execute. Its wrapper also permits arbitrary sibling/text Nodes.
	// Keyed rendering supplies the bounded writer and detached DOM validation.
	var output keyedHTML
	if err := ui.RenderTo(context.Background(), &output, ui.ClientIsland("__nextgo_client_view", nil, node)); err != nil {
		return nil, err
	}
	host := s.Root.Value
	owner := host.Get("ownerDocument")
	detached := owner.Get("implementation").Call("createHTMLDocument", "")
	contextHost := detached.Call("createElement", "div")
	rangeValue := detached.Call("createRange")
	rangeValue.Call("selectNodeContents", contextHost)
	fragment := rangeValue.Call("createContextualFragment", output.String())
	roots, err := keyedChildren(fragment)
	if err != nil || len(roots) != 1 || roots[0].Get("localName").String() != "div" {
		return nil, fmt.Errorf("nextgo: invalid client view tree")
	}
	view := roots[0]
	if view.Get("attributes").Length() != 3 || view.Call("getAttribute", clientBoundaryAttribute).String() != "1" ||
		view.Call("getAttribute", "data-nextgo-island").String() != "__nextgo_client_view" ||
		view.Call("getAttribute", "data-nextgo-props").String() != "null" {
		return nil, fmt.Errorf("nextgo: invalid client view wrapper")
	}
	for _, attr := range []string{clientBoundaryAttribute, "data-nextgo-island", "data-nextgo-props"} {
		view.Call("removeAttribute", attr)
	}
	if err := validateKeyedRow(view); err != nil {
		return nil, fmt.Errorf("nextgo: invalid client view: %w", err)
	}
	if !s.active() || !host.Get("isConnected").Bool() {
		return nil, fmt.Errorf("nextgo: client island was disposed while preparing its view")
	}
	// Rendering is trusted application code and may yield or mutate the host.
	// Revalidate its marker/fallback before opening the DOM transaction.
	if boundary, err := validateClientBoundary(host); err != nil || !boundary {
		return nil, fmt.Errorf("nextgo: client island changed while preparing its view")
	}
	tx := &clientViewTransaction{root: host, view: view, parent: host.Get("parentNode"), fallback: owner.Call("createDocumentFragment"), focus: captureKeyedFocus(host)}
	s.clientView = tx
	for !host.Get("firstChild").IsNull() {
		tx.fallback.Call("appendChild", host.Get("firstChild"))
	}
	host.Call("appendChild", view)
	child, _, err := s.Subscope(Element{view})
	if err != nil {
		return nil, err
	}
	tx.ready = true
	return child, nil
}

func (tx *clientViewTransaction) validateCommit(s *Scope) (err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("nextgo: client view commit panic: %v", failure)
		}
	}()
	if !tx.ready || !s.active() || !tx.root.Get("isConnected").Bool() ||
		!tx.root.Get("parentNode").Equal(tx.parent) ||
		tx.root.Get("childNodes").Length() != 1 || !tx.root.Get("firstChild").Equal(tx.view) {
		return fmt.Errorf("nextgo: client view ownership changed during Mount")
	}
	if tx.root.Call("getAttribute", clientBoundaryAttribute).String() != "1" {
		return fmt.Errorf("nextgo: client boundary marker changed during Mount")
	}
	return nil
}

func (tx *clientViewTransaction) rollback() {
	// Disposal has already run and isolates user cleanup panics. DOM operations
	// here also must not let a broken application-owned host escape mountScoped.
	defer func() { _ = recover() }()
	for !tx.root.Get("firstChild").IsNull() {
		tx.root.Call("removeChild", tx.root.Get("firstChild"))
	}
	tx.root.Call("appendChild", tx.fallback)
	tx.focus.restore()
}

func insideClientFallback(root js.Value) bool {
	parent := root.Get("parentElement")
	return !parent.IsNull() && !parent.IsUndefined() && !parent.Call("closest", "["+clientBoundaryAttribute+"]").IsNull()
}

// Validate ordinary scopes without accessing DOM APIs: existing non-DOM scope
// tests, child scopes and ordinary islands do not opt into replacement behavior.
func validateClientBoundary(root js.Value) (bool, error) {
	if root.Type() != js.TypeObject || root.IsNull() || root.Get("hasAttribute").Type() != js.TypeFunction || !root.Call("hasAttribute", clientBoundaryAttribute).Bool() {
		return false, nil
	}
	if root.Get("localName").String() != "div" || root.Call("getAttribute", clientBoundaryAttribute).String() != "1" ||
		!root.Call("hasAttribute", "data-nextgo-island").Bool() || strings.TrimSpace(root.Call("getAttribute", "data-nextgo-island").String()) == "" {
		return false, fmt.Errorf("nextgo: malformed client island boundary")
	}
	if err := validateClientFallback(root); err != nil {
		return false, err
	}
	return true, nil
}

func validateClientFallback(root js.Value) error {
	if root.Get("childNodes").Length() > 3000 || root.Call("querySelectorAll", "*").Length() > 2000 {
		return fmt.Errorf("nextgo: client fallback node limit exceeded")
	}
	// SSR fallbacks use the same element/attribute/URL validation as fresh keyed
	// trees, with no framework markers at all, including otherwise valid keys.
	nodes := root.Get("childNodes")
	for i := 0; i < nodes.Length(); i++ {
		node := nodes.Index(i)
		switch node.Get("nodeType").Int() {
		case 1:
			if err := validateKeyedRow(node); err != nil {
				return fmt.Errorf("nextgo: invalid client fallback: %w", err)
			}
		case 3:
		default:
			return fmt.Errorf("nextgo: client fallback contains unsupported nodes")
		}
	}
	all := root.Call("querySelectorAll", "*")
	for i := 0; i < all.Length(); i++ {
		attrs := all.Index(i).Get("attributes")
		for j := 0; j < attrs.Length(); j++ {
			if strings.HasPrefix(strings.ToLower(attrs.Index(j).Get("name").String()), "data-nextgo-") {
				return fmt.Errorf("nextgo: client fallback contains framework markers")
			}
		}
	}
	return nil
}
