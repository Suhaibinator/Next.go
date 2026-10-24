//go:build js && wasm

package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/internal/keyed"
	"github.com/Suhaibinator/Next.go/ui"
)

type keyedCloser interface{ Close() }

var ErrKeyedClosed = errors.New("nextgo: keyed collection is closed")
var ErrKeyedReentrant = errors.New("nextgo: keyed collection update is already running")

// KeyedOptions binds behavior to existing SSR rows and renders only added rows.
// Update callbacks must return promptly. A callback panic closes the collection;
// arbitrary application DOM writes cannot be rolled back transactionally.
type KeyedOptions[T any] struct {
	Key      func(T) string
	View     func(T) ui.Node
	Bind     func(*Scope, T) (func(T), error)
	Equal    func(T, T) bool
	MaxItems int
}
type keyedRow[T any] struct {
	root   js.Value
	scope  *Scope
	value  T
	update func(T)
}
type KeyedList[T any] struct {
	scope        *Scope
	host         Element
	options      KeyedOptions[T]
	rows         map[string]*keyedRow[T]
	keys         []string
	closed, busy bool
}

// HydrateKeyed requires an exact ordered key match and keeps the initial nodes.
// A collection owns only direct element children; whitespace text is tolerated.
// Nested islands/stream markers are unsupported within owned rows.
func HydrateKeyed[T any](scope *Scope, host Element, initial []T, options KeyedOptions[T]) (_ *KeyedList[T], err error) {
	if !scope.active() {
		return nil, ErrKeyedClosed
	}
	if options.MaxItems == 0 {
		options.MaxItems = 200
	}
	if options.Key == nil || options.View == nil || options.Bind == nil || options.MaxItems < 1 || options.MaxItems > keyed.MaxItems {
		return nil, fmt.Errorf("nextgo: invalid keyed collection options")
	}
	if scope.keyedCount >= 16 || scope.keyedDepth >= 16 {
		return nil, fmt.Errorf("nextgo: keyed scope bound exceeded")
	}
	if len(initial) > options.MaxItems {
		return nil, fmt.Errorf("nextgo: keyed item limit exceeded")
	}
	var list *KeyedList[T]
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("nextgo: keyed hydration panic (%T)", failure)
		}
		if err != nil && list != nil {
			list.Close()
		}
	}()
	if !scope.ownsElement(host.Value, true) {
		return nil, fmt.Errorf("nextgo: keyed host is outside the scope")
	}
	keys := make([]string, len(initial))
	for i, value := range initial {
		keys[i] = options.Key(value)
	}
	if err := keyed.Validate(keys, options.MaxItems); err != nil {
		return nil, err
	}
	roots, err := keyedChildren(host.Value)
	if err != nil {
		return nil, err
	}
	if len(roots) != len(keys) {
		return nil, fmt.Errorf("nextgo: keyed server row count differs from initial values")
	}
	for i, root := range roots {
		if !root.Call("hasAttribute", "data-nextgo-key").Bool() || root.Call("getAttribute", "data-nextgo-key").String() != keys[i] {
			return nil, fmt.Errorf("nextgo: keyed server order differs from initial values")
		}
		if err := validateKeyedRow(root); err != nil {
			return nil, err
		}
	}
	list = &KeyedList[T]{scope: scope, host: host, options: options, rows: map[string]*keyedRow[T]{}, keys: append([]string(nil), keys...)}
	scope.keyedCount++
	for i, root := range roots {
		row, err := list.bind(root, initial[i])
		if err != nil {
			return nil, err
		}
		list.rows[keys[i]] = row
	}
	if scope.closed {
		return nil, ErrKeyedClosed
	}
	if scope.keyedLists == nil {
		scope.keyedLists = map[keyedCloser]struct{}{}
		scope.Cleanup(func() {
			for owner := range scope.keyedLists {
				owner.Close()
			}
			scope.keyedLists = nil
		})
	}
	scope.keyedLists[list] = struct{}{}
	return list, nil
}
func (l *KeyedList[T]) bind(root js.Value, value T) (_ *keyedRow[T], err error) {
	child := &Scope{parent: l.scope, Root: Element{root}, owner: l.scope.islandRoot(), keyedDepth: l.scope.keyedDepth + 1}
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("nextgo: keyed binding panic (%T)", failure)
		}
		if err != nil {
			child.dispose()
		}
	}()
	update, err := l.options.Bind(child, value)
	if err != nil {
		return nil, err
	}
	return &keyedRow[T]{root: root, scope: child, value: value, update: update}, nil
}
func (l *KeyedList[T]) Keys() []string { return append([]string(nil), l.keys...) }

// Close releases row listeners/effects, leaving their DOM in place. Later Set
// calls fail. Parent-island disposal calls Close automatically.
func (l *KeyedList[T]) Close() {
	if l == nil || l.closed {
		return
	}
	l.closed = true
	for _, row := range l.rows {
		row.scope.dispose()
	}
	l.rows = nil
	l.keys = nil
	delete(l.scope.keyedLists, l)
	if l.scope.keyedCount > 0 {
		l.scope.keyedCount--
	}
	l.scope = nil
	l.host = Element{}
	l.options = KeyedOptions[T]{}
}
func (l *KeyedList[T]) Set(values []T) (err error) {
	if l == nil || l.closed || !l.scope.active() {
		return ErrKeyedClosed
	}
	if l.busy {
		return ErrKeyedReentrant
	}
	l.busy = true
	defer func() { l.busy = false }()
	added := map[string]*keyedRow[T]{}
	committing := false
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("nextgo: keyed update panic (%T)", failure)
			if committing {
				l.Close()
			}
		}
		if err != nil {
			if committing {
				l.Close()
			}
			for _, row := range added {
				row.scope.dispose()
			}
		}
	}()
	if !l.scope.ownsElement(l.host.Value, true) {
		return fmt.Errorf("nextgo: keyed host ownership changed")
	}
	if len(values) > l.options.MaxItems {
		return fmt.Errorf("nextgo: keyed item limit exceeded")
	}
	keys := make([]string, len(values))
	for i, value := range values {
		keys[i] = l.options.Key(value)
	}
	plan, err := keyed.Reconcile(l.keys, keys, l.options.MaxItems)
	if err != nil {
		return err
	}
	current, err := keyedChildren(l.host.Value)
	if err != nil {
		return err
	}
	if len(current) != len(l.keys) {
		return fmt.Errorf("nextgo: keyed DOM was changed outside the collection")
	}
	for i, key := range l.keys {
		if !current[i].Equal(l.rows[key].root) || current[i].Call("getAttribute", "data-nextgo-key").String() != key {
			return fmt.Errorf("nextgo: keyed DOM ownership mismatch")
		}
	}
	total := 0
	for i, key := range keys {
		if _, ok := l.rows[key]; ok {
			continue
		}
		var output keyedHTML
		if err := ui.RenderTo(context.Background(), &output, l.options.View(values[i])); err != nil {
			return err
		}
		total += output.Len()
		if total > 1<<20 {
			return fmt.Errorf("nextgo: keyed additions exceed 1 MiB")
		}
		doc := l.host.Value.Get("ownerDocument").Get("implementation").Call("createHTMLDocument", "")
		contextHost := doc.Call("createElementNS", l.host.Value.Get("namespaceURI"), l.host.Value.Get("localName"))
		r := doc.Call("createRange")
		r.Call("selectNodeContents", contextHost)
		fragment := r.Call("createContextualFragment", output.String())
		roots, err := keyedChildren(fragment)
		if err != nil || len(roots) != 1 {
			return fmt.Errorf("nextgo: keyed view must create one element")
		}
		root := roots[0]
		if !root.Call("hasAttribute", "data-nextgo-key").Bool() || root.Call("getAttribute", "data-nextgo-key").String() != key {
			return fmt.Errorf("nextgo: keyed view key mismatch")
		}
		if err := validateKeyedRow(root); err != nil {
			return err
		}
		row, err := l.bind(root, values[i])
		if err != nil {
			return err
		}
		added[key] = row
	}
	if l.closed || !l.scope.active() {
		return ErrKeyedClosed
	}
	// Bind runs on detached additions. Recheck ownership before changing the host.
	current, err = keyedChildren(l.host.Value)
	if err != nil || len(current) != len(l.keys) {
		return fmt.Errorf("nextgo: keyed host changed during preparation")
	}
	for i, key := range l.keys {
		if !current[i].Equal(l.rows[key].root) {
			return fmt.Errorf("nextgo: keyed host changed during preparation")
		}
	}
	focus := captureKeyedFocus(l.host.Value)
	committing = true
	for _, key := range plan.Removed {
		row := l.rows[key]
		row.scope.dispose()
		l.host.Value.Call("removeChild", row.root)
		delete(l.rows, key)
	}
	for key, row := range added {
		l.rows[key] = row
	}
	anchor := js.Null()
	for i := len(keys) - 1; i >= 0; i-- {
		row := l.rows[keys[i]]
		if !plan.Stable[keys[i]] {
			moveKeyedRow(l.host.Value, row.root, anchor)
		}
		anchor = row.root
	}
	l.keys = keys
	for i, key := range keys {
		row := l.rows[key]
		_, newRow := added[key]
		if !newRow && row.update != nil && (l.options.Equal == nil || !l.options.Equal(row.value, values[i])) {
			row.update(values[i])
		}
		row.value = values[i]
	}
	if l.closed || !l.scope.active() {
		return ErrKeyedClosed
	}
	actual, err := keyedChildren(l.host.Value)
	if err != nil || len(actual) != len(l.keys) {
		return fmt.Errorf("nextgo: update callback changed keyed structure")
	}
	for i, key := range l.keys {
		if !actual[i].Equal(l.rows[key].root) {
			return fmt.Errorf("nextgo: update callback changed keyed structure")
		}
	}
	focus.restore()
	return nil
}

type keyedHTML struct{ buffer strings.Builder }

func (w *keyedHTML) Len() int       { return w.buffer.Len() }
func (w *keyedHTML) String() string { return w.buffer.String() }
func (w *keyedHTML) Write(p []byte) (int, error) {
	if len(p) > 64<<10-w.Len() {
		return 0, fmt.Errorf("nextgo: keyed row exceeds 64 KiB")
	}
	return w.buffer.Write(p)
}
func (w *keyedHTML) WriteString(value string) (int, error) {
	if len(value) > 64<<10-w.Len() {
		return 0, fmt.Errorf("nextgo: keyed row exceeds 64 KiB")
	}
	return w.buffer.WriteString(value)
}
func keyedChildren(parent js.Value) ([]js.Value, error) {
	nodes := parent.Get("childNodes")
	result := []js.Value{}
	if nodes.Length() > 3000 {
		return nil, fmt.Errorf("nextgo: keyed child-node limit exceeded")
	}
	for i := 0; i < nodes.Length(); i++ {
		node := nodes.Index(i)
		switch node.Get("nodeType").Int() {
		case 1:
			result = append(result, node)
		case 3:
			if strings.TrimSpace(node.Get("textContent").String()) != "" {
				return nil, fmt.Errorf("nextgo: keyed host has unowned text")
			}
		default:
			return nil, fmt.Errorf("nextgo: keyed host has unsupported nodes")
		}
	}
	return result, nil
}
func validateKeyedRow(root js.Value) error {
	all := root.Call("querySelectorAll", "*")
	if all.Length() > 2000 {
		return fmt.Errorf("nextgo: keyed row exceeds 2000 descendant elements")
	}
	for i := -1; i < all.Length(); i++ {
		node := root
		if i >= 0 {
			node = all.Index(i)
		}
		tag := strings.ToLower(node.Get("localName").String())
		switch tag {
		case "script", "style", "iframe", "object", "embed", "base", "template", "link", "meta":
			return fmt.Errorf("nextgo: unsupported keyed row content")
		}
		attrs := ui.Attrs{}
		raw := node.Get("attributes")
		for j := 0; j < raw.Length(); j++ {
			attr := raw.Index(j)
			name := attr.Get("name").String()
			if strings.HasPrefix(strings.ToLower(name), "data-nextgo-") && strings.ToLower(name) != "data-nextgo-key" {
				return fmt.Errorf("nextgo: keyed rows cannot contain framework markers")
			}
			attrs[name] = attr.Get("value").String()
		}
		if err := ui.RenderTo(context.Background(), discardKeyed{}, ui.El(tag, attrs)); err != nil {
			return fmt.Errorf("nextgo: unsafe keyed row markup")
		}
	}
	return nil
}

type discardKeyed struct{}

func (discardKeyed) Write(p []byte) (int, error) { return len(p), nil }
func moveKeyedRow(host, row, anchor js.Value) {
	if row.Get("isConnected").Bool() && host.Get("moveBefore").Type() == js.TypeFunction {
		moved := false
		func() { defer func() { _ = recover() }(); host.Call("moveBefore", row, anchor); moved = true }()
		if moved {
			return
		}
	}
	host.Call("insertBefore", row, anchor)
}

type keyedFocus struct {
	node       js.Value
	start, end int
	direction  string
	selection  bool
}

func captureKeyedFocus(host js.Value) keyedFocus {
	node := host.Get("ownerDocument").Get("activeElement")
	f := keyedFocus{node: js.Undefined()}
	if node.IsNull() || node.IsUndefined() || !host.Call("contains", node).Bool() {
		return f
	}
	f.node = node
	if node.Get("selectionStart").Type() == js.TypeNumber && node.Get("selectionEnd").Type() == js.TypeNumber {
		f.start = node.Get("selectionStart").Int()
		f.end = node.Get("selectionEnd").Int()
		f.direction = node.Get("selectionDirection").String()
		f.selection = true
	}
	return f
}
func (f keyedFocus) restore() {
	if f.node.IsUndefined() || !f.node.Get("isConnected").Bool() {
		return
	}
	if !f.node.Get("ownerDocument").Get("activeElement").Equal(f.node) {
		f.node.Call("focus", map[string]any{"preventScroll": true})
	}
	if f.selection {
		f.node.Call("setSelectionRange", f.start, f.end, f.direction)
	}
}
