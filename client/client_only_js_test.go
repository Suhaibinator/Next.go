//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/ui"
)

// This DOM double exercises transaction/ownership behavior under the existing
// Node-hosted WASM unit suite. Its parser returns an explicit prepared tree and
// records the markup; real HTML parsing, WASM delays and lazy bridge integration
// require the separate browser suite and are not claimed by these unit tests.
func clientOnlyDOM(t *testing.T) js.Value {
	t.Helper()
	return js.Global().Get("Function").New(`
const fixture = {};
let owner;
class Node extends EventTarget {
  constructor(tag, attrs = {}, type = 1) {
    super(); this.localName = tag; this.nodeType = type;
    this._attrs = {...attrs}; this.childNodes = []; this.parentNode = null;
    this.ownerDocument = owner; this.namespaceURI = 'http://www.w3.org/1999/xhtml';
    this.listeners = 0; this.textContent = '';
  }
  get attributes() { return Object.entries(this._attrs).map(([name,value])=>({name,value})); }
  get firstChild() { return this.childNodes[0] || null; }
  get parentElement() { return this.parentNode && this.parentNode.nodeType === 1 ? this.parentNode : null; }
  get isConnected() { return !!this._connected || !!(this.parentNode && this.parentNode.isConnected); }
  hasAttribute(name) { return Object.hasOwn(this._attrs, name); }
  getAttribute(name) { return this.hasAttribute(name) ? this._attrs[name] : null; }
  setAttribute(name, value) { this._attrs[name] = String(value); }
  removeAttribute(name) { delete this._attrs[name]; }
  appendChild(node) {
    if (node.nodeType === 11) { for (const child of [...node.childNodes]) this.appendChild(child); return node; }
    if (node.parentNode) node.parentNode.removeChild(node);
    this.childNodes.push(node); node.parentNode = this;
    const adopt = n => { n.ownerDocument = this.ownerDocument; for (const child of n.childNodes) adopt(child); };
    adopt(node); return node;
  }
  removeChild(node) {
    const index = this.childNodes.indexOf(node); if (index < 0) throw Error('not a child');
    if (this.ownerDocument.activeElement && node.contains(this.ownerDocument.activeElement)) this.ownerDocument.activeElement = null;
    this.childNodes.splice(index, 1); node.parentNode = null; return node;
  }
  contains(node) { return node === this || this.childNodes.some(child => child.contains(node)); }
  matches(selector) {
    if (selector === '*') return this.nodeType === 1;
    const match = /^\[([^\]=]+)\]$/.exec(selector);
    return this.nodeType === 1 && (match ? this.hasAttribute(match[1]) : this.localName === selector);
  }
  querySelectorAll(selector) {
    const found = []; const walk = n => { for (const child of n.childNodes) { if (child.matches(selector)) found.push(child); walk(child); } };
    walk(this); return found;
  }
  closest(selector) { for (let node = this; node; node = node.parentElement) if (node.matches(selector)) return node; return null; }
  addEventListener(...args) { super.addEventListener(...args); this.listeners++; }
  removeEventListener(...args) { super.removeEventListener(...args); this.listeners--; }
  focus() { this.ownerDocument.activeElement = this; }
  setSelectionRange(start,end,direction) { this.selectionStart=start;this.selectionEnd=end;this.selectionDirection=direction; }
}
owner = {
  activeElement: null,
  createDocumentFragment() { return new Node('', {}, 11); },
  createElement(tag) { return new Node(tag); },
  implementation: { createHTMLDocument() {
    fixture.detachedParses++;
    return {createElement(tag) { return new Node(tag); }, createRange() {
      return {selectNodeContents() {}, createContextualFragment(html) {
        fixture.rendered = html;
        const fragment = new Node('', {}, 11); fragment.appendChild(fixture.fresh); return fragment;
      }};
    }};
  }}
};
fixture.detachedParses = 0;
fixture.owner = owner;
fixture.outer = new Node('main'); fixture.outer._connected = true;
fixture.root = new Node('div', {'data-nextgo-client':'1','data-nextgo-island':'counter','data-nextgo-props':'null'});
fixture.outer.appendChild(fixture.root);
fixture.fallback = new Node('input', {'data-fallback':''}); fixture.fallback.value = 'typed before WASM';
fixture.fallback.setSelectionRange(2,5,'forward');
fixture.root.appendChild(fixture.fallback); fixture.fallback.focus();
fixture.fresh = new Node('div', {'data-nextgo-client':'1','data-nextgo-island':'__nextgo_client_view','data-nextgo-props':'null'});
fixture.button = new Node('button', {'data-click':''}); fixture.fresh.appendChild(fixture.button);
fixture.extra = new Node('p');
fixture.make = (tag, attrs={}) => new Node(tag, attrs);
return fixture;
`).Invoke()
}

func TestClientViewCommitsOwnedLiveTreeAndDisposesListeners(t *testing.T) {
	f := clientOnlyDOM(t)
	scope := &Scope{Root: Element{f.Get("root")}}
	var view *Scope
	calls := 0
	err := mountScoped(scope, func(s *Scope, _ json.RawMessage) error {
		var err error
		view, err = s.ClientView(ui.El("button", ui.Attrs{"data-click": ""}, ui.Text("Go counter")))
		if err != nil {
			return err
		}
		if !view.Root.Value.Get("isConnected").Bool() || view.parent != s {
			t.Fatal("view was not connected and owned during binding")
		}
		button, err := view.Require("[data-click]")
		if err != nil {
			return err
		}
		view.On(button, "click", func(Event) { calls++ })
		if _, err := s.Require("[data-click]"); err == nil {
			t.Fatal("parent queried child-owned tree")
		}
		if _, err := s.ClientView(ui.Text("second")); err == nil {
			t.Fatal("second ClientView accepted")
		}
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !scope.Root.Value.Equal(f.Get("root")) || !scope.Root.Value.Get("firstChild").Equal(f.Get("fresh")) ||
		f.Get("fallback").Get("isConnected").Bool() || scope.clientView != nil || scope.mountOpen || scope.mountClient {
		t.Fatal("mount replaced the boundary, kept fallback, or retained a transaction")
	}
	if refreshScopeSupported(scope) || refreshScopeSupported(view) {
		t.Fatal("client tree incorrectly eligible for refresh transfer")
	}
	if !strings.Contains(f.Get("rendered").String(), "Go counter") || f.Get("detachedParses").Int() != 1 {
		t.Fatal("view did not use bounded rendering/detached parser")
	}
	f.Get("button").Call("dispatchEvent", js.Global().Get("Event").New("click"))
	if calls != 1 || f.Get("button").Get("listeners").Int() != 1 {
		t.Fatal("Go event did not bind", calls)
	}
	scope.dispose()
	f.Get("button").Call("dispatchEvent", js.Global().Get("Event").New("click"))
	if calls != 1 || f.Get("button").Get("listeners").Int() != 0 || !view.closed || !scope.Root.Value.Get("firstChild").Equal(f.Get("fresh")) {
		t.Fatal("unmount leaked behavior or restored fallback after a committed mount", calls)
	}
}

func TestClientViewRestoresExactFallbackAfterErrorPanicOrOwnershipLoss(t *testing.T) {
	for _, failure := range []string{"error", "panic", "ownership", "disposed"} {
		t.Run(failure, func(t *testing.T) {
			f := clientOnlyDOM(t)
			scope := &Scope{Root: Element{f.Get("root")}}
			calls, cleanups := 0, 0
			var child *Scope
			err := mountScoped(scope, func(s *Scope, _ json.RawMessage) error {
				var err error
				child, err = s.ClientView(ui.El("button", ui.Attrs{"data-click": ""}))
				if err != nil {
					return err
				}
				button, err := child.Require("[data-click]")
				if err != nil {
					return err
				}
				child.On(button, "click", func(Event) { calls++ })
				button.Value.Call("focus")
				f.Get("fallback").Call("setSelectionRange", 0, 0, "backward")
				child.Cleanup(func() { cleanups++ })
				switch failure {
				case "error":
					return errors.New("bind failure")
				case "panic":
					panic("bind panic")
				case "ownership":
					s.Root.Value.Call("appendChild", f.Get("extra"))
				case "disposed":
					s.dispose()
				}
				return nil
			}, nil)
			if err == nil || !scope.closed || child == nil || !child.closed || cleanups != 1 || scope.clientView != nil {
				t.Fatal("partial scope survived", err, cleanups)
			}
			if !scope.Root.Value.Get("firstChild").Equal(f.Get("fallback")) || scope.Root.Value.Get("childNodes").Length() != 1 ||
				f.Get("fallback").Get("value").String() != "typed before WASM" || !f.Get("owner").Get("activeElement").Equal(f.Get("fallback")) {
				t.Fatal("fallback identity, edits or focus lost")
			}
			if f.Get("fallback").Get("selectionStart").Int() != 2 || f.Get("fallback").Get("selectionEnd").Int() != 5 {
				t.Fatal("fallback selection lost")
			}
			f.Get("button").Call("dispatchEvent", js.Global().Get("Event").New("click"))
			if calls != 0 || f.Get("button").Get("listeners").Int() != 0 {
				t.Fatal("detached view kept a listener")
			}
			scope.dispose()
			if cleanups != 1 {
				t.Fatal("rollback disposed twice")
			}
		})
	}
}

func TestClientViewRejectsInvalidBoundaryAndPreparedMarkupBeforeDOMCommit(t *testing.T) {
	for _, invalid := range []string{"marker", "fallback-island", "fallback-key", "fallback-url", "fallback-handler", "view-island", "view-script", "view-wrapper"} {
		t.Run(invalid, func(t *testing.T) {
			f := clientOnlyDOM(t)
			switch invalid {
			case "marker":
				f.Get("root").Call("setAttribute", clientBoundaryAttribute, "2")
			case "fallback-island":
				f.Get("fallback").Call("setAttribute", "data-nextgo-island", "nested")
			case "fallback-key":
				f.Get("fallback").Call("setAttribute", "data-nextgo-key", "nested")
			case "fallback-url":
				f.Get("fallback").Call("setAttribute", "src", "javascript:alert(1)")
			case "fallback-handler":
				f.Get("fallback").Call("setAttribute", "onclick", "alert(1)")
			case "view-island":
				f.Get("button").Call("setAttribute", "data-nextgo-island", "nested")
			case "view-script":
				f.Get("fresh").Call("appendChild", f.Get("make").Invoke("script"))
			case "view-wrapper":
				f.Get("fresh").Call("setAttribute", "id", "unexpected")
			}
			scope := &Scope{Root: Element{f.Get("root")}}
			err := mountScoped(scope, func(s *Scope, _ json.RawMessage) error {
				_, err := s.ClientView(ui.Text("view"))
				return err
			}, nil)
			if err == nil || !scope.closed || !scope.Root.Value.Get("firstChild").Equal(f.Get("fallback")) || f.Get("button").Get("listeners").Int() != 0 {
				t.Fatal("invalid markup changed the fallback", invalid, err)
			}
		})
	}
}

func TestClientViewRequiresMountBoundaryAndImmediateBoundedUI(t *testing.T) {
	var absent *Scope
	if _, err := absent.ClientView(ui.Text("no")); err == nil {
		t.Fatal("nil scope accepted")
	}
	f := clientOnlyDOM(t)
	scope := &Scope{Root: Element{f.Get("root")}}
	if _, err := scope.ClientView(ui.Text("outside")); err == nil || scope.clientViewUsed {
		t.Fatal("outside-Mount call accepted or consumed the slot")
	}
	f.Get("root").Call("removeAttribute", clientBoundaryAttribute)
	if err := mountScoped(scope, func(s *Scope, _ json.RawMessage) error {
		if _, err := s.ClientView(ui.Text("ordinary")); err == nil {
			t.Fatal("ordinary hydration permitted DOM replacement")
		}
		return nil
	}, nil); err != nil || !scope.Root.Value.Get("firstChild").Equal(f.Get("fallback")) {
		t.Fatal("ordinary island changed", err)
	}
	for _, invalid := range []string{"missing-call", "nil-view", "large", "many-elements", "nested-island", "deferred"} {
		t.Run(invalid, func(t *testing.T) {
			f := clientOnlyDOM(t)
			scope := &Scope{Root: Element{f.Get("root")}}
			loads := 0
			err := mountScoped(scope, func(s *Scope, _ json.RawMessage) error {
				var view ui.Node
				switch invalid {
				case "missing-call":
					return nil
				case "large":
					view = ui.Text(strings.Repeat("x", 64<<10+1))
				case "many-elements":
					children := make([]ui.Node, 2001)
					for i := range children {
						children[i] = ui.El("i", nil)
					}
					view = ui.Fragment(children...)
				case "nested-island":
					view = ui.Island("nested", nil, ui.Text("no"))
				case "deferred":
					view = ui.El("section", nil, ui.Defer("bad", ui.Text("wait"), func(context.Context) (ui.Node, error) { loads++; return ui.Text("loaded"), nil }))
				}
				_, err := s.ClientView(view)
				if invalid == "nil-view" {
					return nil // Even a swallowed preparation error cannot commit.
				}
				return err
			}, nil)
			if err == nil || !scope.closed || loads != 0 || f.Get("detachedParses").Int() != 0 || !scope.Root.Value.Get("firstChild").Equal(f.Get("fallback")) {
				t.Fatal("invalid view published DOM or executed deferred work", err, loads)
			}
		})
	}
}

func TestClientFallbackAncestryMustBeRecordedBeforeReplacement(t *testing.T) {
	f := clientOnlyDOM(t)
	nested := f.Get("make").Invoke("div", map[string]any{"data-nextgo-island": "nested"})
	f.Get("root").Call("appendChild", nested)
	if !insideClientFallback(nested) || insideClientFallback(f.Get("root")) {
		t.Fatal("fallback/root ancestry was misclassified")
	}
	// A querySelectorAll NodeList retains this node after the fallback is moved.
	// The original ancestry snapshot must suppress it even though closest no
	// longer reaches the client boundary after detachment.
	recorded := insideClientFallback(nested)
	fragment := f.Get("owner").Call("createDocumentFragment")
	fragment.Call("appendChild", nested)
	if !recorded || insideClientFallback(nested) {
		t.Fatal("ancestry fixture does not exercise detached snapshot behavior")
	}
}

// Goexit runs defers without a recoverable panic. It must not publish a partial
// client view or retain effects merely because Mount never returned an error.
func TestClientViewGoexitRollsBackAndDisposes(t *testing.T) {
	f := clientOnlyDOM(t)
	scope := &Scope{Root: Element{f.Get("root")}}
	cleanups := 0
	var failure error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = mountScoped(scope, func(s *Scope, _ json.RawMessage) error {
			child, err := s.ClientView(ui.Text("fresh"))
			if err != nil {
				failure = err
				return err
			}
			child.Cleanup(func() { cleanups++ })
			runtime.Goexit()
			return nil
		}, nil)
	}()
	<-done
	if failure != nil || !scope.closed || scope.clientView != nil || cleanups != 1 || !scope.Root.Value.Get("firstChild").Equal(f.Get("fallback")) {
		t.Fatal("abnormal mount exit committed or leaked its view", failure, cleanups)
	}
}
