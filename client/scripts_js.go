//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Suhaibinator/Next.go/scripts"
	"strings"
	"syscall/js"
	"time"
)

const maxScriptWaiters = 256

type scriptLoad struct {
	declaration scripts.Script
	done        chan struct{}
	promote     chan struct{}
	err         error
	early       bool
	node        js.Value
}

var scriptLoads = map[string]*scriptLoad{}
var scriptAliases = map[string]string{}
var scriptSources = map[string]string{}
var scriptSlots = make(chan struct{}, 4)
var scriptWaiters int

func scriptNonce() (value string) {
	defer func() { _ = recover() }()
	node := doc.Call("querySelector", "script[data-nextgo-bootstrap]")
	if node.Truthy() {
		return node.Get("nonce").String()
	}
	return ""
}
func resolveScript(input scripts.Script) (scripts.Script, error) {
	s, err := input.Check()
	if err != nil {
		return s, err
	}
	if s.Asset {
		s.Src, err = AssetURL(s.Src)
		if err != nil {
			return s, err
		}
		s.Asset = false
	}
	s.Src = win.Get("URL").New(s.Src, doc.Get("baseURI")).Get("href").String()
	return s.Check()
}
func checkScriptIdentity(s scripts.Script) error {
	key := s.Key()
	if old, ok := scriptAliases[s.ID]; ok && old != key {
		return fmt.Errorf("scripts: ID already names another dependency")
	}
	if old, ok := scriptSources[s.Src]; ok && old != key {
		return fmt.Errorf("scripts: source already has different security or module attributes")
	}
	if _, ok := scriptAliases[s.ID]; !ok && len(scriptAliases) >= 128 {
		return fmt.Errorf("scripts: document ID limit reached")
	}
	if scriptLoads[key] == nil && len(scriptLoads) >= 64 {
		return fmt.Errorf("scripts: document dependency limit reached")
	}
	return nil
}

// LoadScript waits for one explicit trusted dependency. It deduplicates by
// resolved source/security attributes for the document lifetime. Canceling a
// caller stops its wait; it cannot revoke code already downloaded/executed.
// Call from a goroutine. Use Scope.Script for owned ready/error callbacks.
func LoadScript(ctx context.Context, input scripts.Script) error {
	return loadScript(ctx, input, false)
}

// RetryScript retries a failed download. It never reexecutes a successful script.
func RetryScript(ctx context.Context, input scripts.Script) error {
	return loadScript(ctx, input, true)
}
func loadScript(ctx context.Context, input scripts.Script, retry bool) (err error) {
	if ctx == nil {
		return fmt.Errorf("scripts: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	checked, err := input.Check()
	if err != nil {
		return err
	}
	if checked.Strategy == scripts.BeforeInteractive {
		return fmt.Errorf("scripts: early dependencies are declared in Config.EarlyScripts; check their library API directly")
	}
	if moduleMode {
		return loadScriptFromHost(ctx, checked, retry)
	}
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("scripts: browser setup failed: %v", failure)
		}
	}()
	if currentURL == "" {
		return fmt.Errorf("scripts: document runtime has not started")
	}
	if scriptWaiters >= maxScriptWaiters {
		return fmt.Errorf("scripts: waiter limit reached")
	}
	scriptWaiters++
	defer func() { scriptWaiters-- }()
	checked, err = resolveScript(checked)
	if err != nil {
		return err
	}
	if err = checkScriptIdentity(checked); err != nil {
		return err
	}
	key := checked.Key()
	state := scriptLoads[key]
	if state != nil && state.early {
		return fmt.Errorf("scripts: this source belongs to an early declaration; check its library API directly")
	}
	if state != nil && retry {
		select {
		case <-state.done:
			if state.err != nil {
				if state.node.Truthy() {
					state.node.Call("remove")
				}
				delete(scriptLoads, key)
				state = nil
			}
		default:
		}
	}
	scriptAliases[checked.ID] = key
	scriptSources[checked.Src] = key
	if state == nil {
		state = &scriptLoad{declaration: checked, done: make(chan struct{}), promote: make(chan struct{}, 1), node: js.Undefined()}
		scriptLoads[key] = state
		go func() {
			defer close(state.done)
			defer func() {
				if failure := recover(); failure != nil {
					state.err = fmt.Errorf("scripts: startup failed: %v", failure)
				}
			}()
			state.err = downloadScript(state)
		}()
	} else if checked.Strategy == scripts.AfterInteractive {
		select {
		case state.promote <- struct{}{}:
		default:
		}
	}
	select {
	case <-state.done:
		return state.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Scope) Script(input scripts.Script, ready func(error)) error {
	if !s.active() || ready == nil {
		return fmt.Errorf("scripts: require an open scope and callback")
	}
	checked, err := input.Check()
	if err != nil {
		return err
	}
	if checked.Strategy == scripts.BeforeInteractive {
		return fmt.Errorf("scripts: early declarations belong in Config.EarlyScripts")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.Cleanup(cancel)
	go func() {
		err := LoadScript(ctx, checked)
		if ctx.Err() != nil || !s.active() {
			return
		}
		defer func() {
			if failure := recover(); failure != nil {
				report(fmt.Sprintf("script callback: %v", failure))
			}
		}()
		ready(err)
	}()
	return nil
}
func downloadScript(state *scriptLoad) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if state.declaration.Strategy == scripts.LazyOnload {
		if err := scriptIdle(ctx, state.promote); err != nil {
			return err
		}
	}
	select {
	case scriptSlots <- struct{}{}:
		defer func() { <-scriptSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	node := doc.Call("createElement", "script")
	state.node = node
	node.Call("setAttribute", "data-nextgo-dependency", state.declaration.ID)
	if nonce := scriptNonce(); nonce != "" {
		node.Set("nonce", nonce)
	}
	d := state.declaration
	for _, attr := range [][2]string{{"type", d.Type}, {"integrity", d.Integrity}, {"crossorigin", d.CrossOrigin}, {"referrerpolicy", d.ReferrerPolicy}} {
		if attr[1] != "" {
			node.Call("setAttribute", attr[0], attr[1])
		}
	}
	finished := make(chan error, 1)
	loaded := js.FuncOf(func(js.Value, []js.Value) any {
		select {
		case finished <- nil:
		default:
		}
		return nil
	})
	failed := js.FuncOf(func(js.Value, []js.Value) any {
		select {
		case finished <- fmt.Errorf("scripts: dependency failed to load; check URL, integrity and CSP"):
		default:
		}
		return nil
	})
	node.Call("addEventListener", "load", loaded)
	node.Call("addEventListener", "error", failed)
	defer func() {
		node.Call("removeEventListener", "load", loaded)
		node.Call("removeEventListener", "error", failed)
		loaded.Release()
		failed.Release()
	}()
	node.Call("setAttribute", "src", d.Src)
	doc.Get("head").Call("appendChild", node)
	select {
	case err := <-finished:
		if err != nil {
			node.Call("remove")
		}
		return err
	case <-ctx.Done():
		node.Call("remove")
		return ctx.Err()
	}
}
func scriptIdle(ctx context.Context, promote <-chan struct{}) error {
	if doc.Get("readyState").String() != "complete" {
		loaded := make(chan struct{}, 1)
		fn := js.FuncOf(func(js.Value, []js.Value) any {
			select {
			case loaded <- struct{}{}:
			default:
			}
			return nil
		})
		win.Call("addEventListener", "load", fn)
		defer func() { win.Call("removeEventListener", "load", fn); fn.Release() }()
		select {
		case <-loaded:
		case <-promote:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if win.Get("requestIdleCallback").Type() != js.TypeFunction {
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil
		case <-promote:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	idle := make(chan struct{}, 1)
	fn := js.FuncOf(func(js.Value, []js.Value) any {
		select {
		case idle <- struct{}{}:
		default:
		}
		return nil
	})
	id := win.Call("requestIdleCallback", fn, map[string]any{"timeout": 1000})
	defer func() { win.Call("cancelIdleCallback", id); fn.Release() }()
	select {
	case <-idle:
		return nil
	case <-promote:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type scriptMarker struct {
	node   js.Value
	script scripts.Script
}

func scriptMarkers(root js.Value) ([]scriptMarker, error) {
	nodes := root.Call("querySelectorAll", "[data-nextgo-script]")
	if nodes.Length() > 64 {
		return nil, fmt.Errorf("scripts: too many active declarations")
	}
	var result []scriptMarker
	newKeys, newIDs := map[string]bool{}, map[string]bool{}
	ids, sources := map[string]string{}, map[string]string{}
	for i := 0; i < nodes.Length(); i++ {
		node := nodes.Index(i)
		if strings.ToLower(node.Get("tagName").String()) != "nextgo-script" || node.Get("childNodes").Length() != 0 {
			return nil, fmt.Errorf("scripts: malformed declaration element")
		}
		s, err := scripts.Parse(node.Call("getAttribute", "data-nextgo-script").String())
		if err != nil {
			return nil, err
		}
		if s.Strategy == scripts.BeforeInteractive {
			return nil, fmt.Errorf("scripts: early declarations cannot appear in route content")
		}
		s, err = resolveScript(s)
		if err != nil {
			return nil, err
		}
		if err = checkScriptIdentity(s); err != nil {
			return nil, err
		}
		key := s.Key()
		if old, ok := ids[s.ID]; ok && old != key {
			return nil, fmt.Errorf("scripts: conflicting IDs in route")
		}
		if old, ok := sources[s.Src]; ok && old != key {
			return nil, fmt.Errorf("scripts: conflicting source attributes in route")
		}
		ids[s.ID] = key
		sources[s.Src] = key
		if scriptLoads[key] == nil {
			newKeys[key] = true
		}
		if _, ok := scriptAliases[s.ID]; !ok {
			newIDs[s.ID] = true
		}
		result = append(result, scriptMarker{node, s})
	}
	if len(scriptLoads)+len(newKeys) > 64 || len(scriptAliases)+len(newIDs) > 128 {
		return nil, fmt.Errorf("scripts: route exceeds document dependency/ID limits")
	}
	return result, nil
}
func hydrateScripts() error {
	markers, err := scriptMarkers(doc.Call("getElementById", "nextgo-app"))
	if err != nil {
		return err
	}
	for _, marker := range markers {
		node := marker.node
		if node.Call("hasAttribute", "data-nextgo-script-state").Bool() {
			continue
		}
		scope := &Scope{Root: Element{node}}
		node.Call("setAttribute", "data-nextgo-script-state", "loading")
		if err := scope.Script(marker.script, func(err error) {
			if err != nil {
				node.Call("setAttribute", "data-nextgo-script-state", "error")
				win.Get("console").Call("error", "Next.go: "+err.Error())
			} else {
				node.Call("setAttribute", "data-nextgo-script-state", "loaded")
			}
		}); err != nil {
			scope.dispose()
			return err
		}
		mounted = append(mounted, scope)
	}
	return nil
}
func primeEarlyScripts() error {
	nodes := doc.Call("querySelectorAll", "script[data-nextgo-early]")
	if nodes.Length() > 16 {
		return fmt.Errorf("scripts: too many early declarations")
	}
	for i := 0; i < nodes.Length(); i++ {
		s, err := scripts.Parse(nodes.Index(i).Call("getAttribute", "data-nextgo-early").String())
		if err != nil {
			return err
		}
		if s.Strategy != scripts.BeforeInteractive || s.Asset {
			return fmt.Errorf("scripts: invalid early declaration")
		}
		s.Src = win.Get("URL").New(s.Src, doc.Get("baseURI")).Get("href").String()
		s, err = s.Check()
		if err != nil {
			return err
		}
		if err = checkScriptIdentity(s); err != nil {
			return err
		}
		key := s.Key()
		scriptAliases[s.ID] = key
		scriptSources[s.Src] = key
		scriptLoads[key] = &scriptLoad{declaration: s, early: true, node: nodes.Index(i)}
	}
	return nil
}
func loadScriptFromHost(ctx context.Context, s scripts.Script, retry bool) (err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("scripts: component host unavailable: %v", failure)
		}
	}()
	data, _ := json.Marshal(s)
	promise := moduleHost.Call("script", string(data), retry)
	controller := win.Get("AbortController").New()
	defer controller.Call("abort")
	_, err = awaitBrowserPromise(ctx, promise, controller)
	return err
}
func scriptHostPromise(args []js.Value) any {
	var executor js.Func
	executor = js.FuncOf(func(_ js.Value, promiseArgs []js.Value) any {
		resolve, reject := promiseArgs[0], promiseArgs[1]
		raw, retry := args[0].String(), args[1].Bool()
		go func() {
			s, err := scripts.Parse(raw)
			if err == nil {
				err = loadScript(context.Background(), s, retry)
			}
			if err != nil {
				reject.Invoke(err.Error())
			} else {
				resolve.Invoke(js.Null())
			}
		}()
		return nil
	})
	promise := win.Get("Promise").New(executor)
	executor.Release()
	return promise
}
