//go:build js && wasm

package client

import (
	"context"
	"syscall/js"
	"testing"
	"time"
)

func resourceOriginNode() js.Value { return js.Global().Get("Object").New() }
func resourceOriginView(t *testing.T, root js.Value, body string) (*Scope, *ResourceSource, *AsyncResource) {
	t.Helper()
	scope := &Scope{Root: Element{root}}
	t.Cleanup(scope.dispose)
	store, err := scope.ResourceStore(ResourceStoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	source := resourceSource(t, store, "origin-public", time.Minute, func(context.Context) ([]byte, error) { return []byte("fresh HTTP value"), nil })
	view, err := scope.AsyncResource(source, AsyncResourceOptions{Seed: &ResourceSeed{Key: "origin-public", Body: []byte(body), ExpiresUnixMilli: time.Now().Add(30 * time.Second).UnixMilli()}})
	if err != nil {
		t.Fatal(err)
	}
	return scope, source, view
}
func checkResourceLateSSRSeedOrigin(t *testing.T) {
	t.Helper()
	root := resourceOriginNode()
	recordResourceOrigin(root)
	before := root.Get(resourceOriginProperty).String()
	// SSR DOM/props are present, but a lazy download has not yet mounted them.
	invalidateResourceStores()
	first, source, view := resourceOriginView(t, root, "old server value")
	if snapshot := view.Snapshot(); snapshot.Phase != ResourceInvalidated || snapshot.Seeded || len(snapshot.Body) != 0 {
		t.Fatal("delayed seed crossed mutation fence", snapshot)
	}
	// An explicit load is still usable, and another view can adopt its fresh
	// public cache entry rather than remaining permanently invalidated.
	if err := view.Load(); err != nil {
		t.Fatal(err)
	}
	resourceWait(t, func() bool { return view.Snapshot().Phase == ResourceReady })
	another, err := first.AsyncResource(source, AsyncResourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := another.Snapshot(); snapshot.Phase != ResourceReady || string(snapshot.Body) != "fresh HTTP value" {
		t.Fatal("fresh loaded value not adoptable", snapshot)
	}
	first.dispose()
	// A failed lazy download can dispose/recreate Scope on the same DOM node.
	// Retrying must retain that node's original epoch rather than blessing it.
	recordResourceOrigin(root)
	if root.Get(resourceOriginProperty).String() != before {
		t.Fatal("same-node lazy retry changed origin")
	}
	_, _, retry := resourceOriginView(t, root, "old retry value")
	if retry.Snapshot().Phase != ResourceInvalidated || retry.Snapshot().Seeded {
		t.Fatal("same-node retry adopted old SSR seed")
	}
	// A new navigation delivers a different node after the fence. Its explicit
	// new seed remains useful despite the document epoch being nonzero.
	fresh := resourceOriginNode()
	recordResourceOrigin(fresh)
	_, _, current := resourceOriginView(t, fresh, "new navigation value")
	if snapshot := current.Snapshot(); snapshot.Phase != ResourceReady || !snapshot.Seeded || string(snapshot.Body) != "new navigation value" {
		t.Fatal("fresh navigation seed was disabled", snapshot)
	}
}
func TestResourceLateSSRSeedOrigin(t *testing.T) { checkResourceLateSSRSeedOrigin(t) }
func TestResourceOriginDescendantAndMalformedMetadata(t *testing.T) {
	root := resourceOriginNode()
	recordResourceOrigin(root)
	invalidateResourceStores()
	parent := &Scope{Root: Element{root}}
	child := &Scope{parent: parent, Root: Element{resourceOriginNode()}}
	t.Cleanup(parent.dispose)
	t.Cleanup(child.dispose)
	store, err := child.ResourceStore(ResourceStoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	source := resourceSource(t, store, "descendant", time.Minute, func(context.Context) ([]byte, error) { return nil, nil })
	view, err := child.AsyncResource(source, AsyncResourceOptions{Seed: &ResourceSeed{Key: "descendant", Body: []byte("old parent seed"), ExpiresUnixMilli: time.Now().Add(30 * time.Second).UnixMilli()}})
	if err != nil {
		t.Fatal(err)
	}
	if view.Snapshot().Phase != ResourceInvalidated || view.Snapshot().Seeded {
		t.Fatal("descendant escaped owning-island origin")
	}
	for _, value := range []any{nil, "malformed", "18446744073709551616"} {
		node := resourceOriginNode()
		if value != nil {
			node.Set(resourceOriginProperty, value)
		}
		_, _, view := resourceOriginView(t, node, "untrusted metadata")
		if view.Snapshot().Phase != ResourceInvalidated || view.Snapshot().Seeded {
			t.Fatal("missing/malformed origin accepted", value)
		}
	}
}
