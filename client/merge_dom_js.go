//go:build js && wasm

package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/ui"
)

type mergeCarrier struct {
	old, fresh   js.Value
	anchors      []*mergeCarrier
	rootBoundary *mergeBoundary
}
type mergeDOMPlan struct {
	previous, next                 *mergeDocument
	tree                           *mergeCarrier
	parent                         js.Value
	candidates                     map[string]*mergeBoundary
	freshRoots                     map[string]js.Value
	retiring                       []*Scope
	prepared                       []*preparedProps
	cohort                         *propsCohort
	scopes                         []*Scope
	staticBytes                    int
	beforeID, beforeEpoch, privacy uint64
	target                         string
	installed                      bool
	fence                          *mergePreparationFence
}

var errMergeUnsupported = errors.New("nextgo: merge structure is unsupported")
var errMergeOwnership = errors.New("nextgo: application owner changed during preparation")

// Count queued and completed nodes before expanding, selecting or cloning.
// This bounds our work after the existing navigation HTML parser returns;
// it does not claim that parsing arbitrary raw HTML allocated no nodes.
func mergeBoundedNodes(root js.Value, limit int) (int, error) {
	type pendingNode struct {
		node  js.Value
		depth int
	}
	pending := []pendingNode{{root, 0}}
	count := 0
	for len(pending) > 0 {
		entry := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		count++
		if count > limit || entry.depth > 128 {
			return count, errRetainedCapacity
		}
		children := entry.node.Get("childNodes")
		n := children.Length()
		if n > limit-count-len(pending) {
			return limit + 1, errRetainedCapacity
		}
		for i := 0; i < n; i++ {
			pending = append(pending, pendingNode{children.Index(i), entry.depth + 1})
		}
	}
	return count, nil
}

// Fallback reconstruction parses markup, never clones live FileList/value
// properties. Initial trusted DOM serialization is NOT an integrity digest;
// fetched roots use their exact validated original server byte range.
func mergeRangeFallback(value *navigation.MergeDescriptor, island navigation.MergeIsland, node js.Value, raw string) (js.Value, error) {
	snippet := ""
	if raw == "" {
		snippet = node.Get("outerHTML").String()
	} else {
		if island.Start < 0 || island.End > len(raw) || island.Start >= island.End {
			return js.Undefined(), fmt.Errorf("invalid merge raw range")
		}
		snippet = raw[island.Start:island.End]
	}
	parsed := win.Get("DOMParser").New().Call("parseFromString", snippet, "text/html")
	body := parsed.Get("body")
	children := body.Get("childNodes")
	if children.Length() != 1 || children.Index(0).Get("nodeType").Int() != 1 || !children.Index(0).Call("isEqualNode", node).Bool() {
		return js.Undefined(), fmt.Errorf("merge raw range does not identify parsed boundary")
	}
	fallback := children.Index(0)
	body.Call("removeChild", fallback)
	return fallback, nil
}
func mergeHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func mergeChildren(parent js.Value) []js.Value {
	list := parent.Get("childNodes")
	out := make([]js.Value, list.Length())
	for i := range out {
		out[i] = list.Index(i)
	}
	return out
}
func mergeSameNode(a, b js.Value) bool {
	return a.Type() == js.TypeObject && b.Type() == js.TypeObject && !a.IsNull() && !b.IsNull() && a.Equal(b)
}
func mergeByRoot(node js.Value, roots map[string]js.Value) string {
	for address, root := range roots {
		if mergeSameNode(node, root) {
			return address
		}
	}
	return ""
}
func mergeScopeFor(root js.Value) *Scope {
	for _, scope := range mounted {
		if mergeSameNode(scope.Root.Value, root) {
			return scope
		}
	}
	return nil
}
func mergeKeyAncestry(value *navigation.MergeDescriptor, island navigation.MergeIsland, root, stop js.Value) error {
	type item struct{ key, tag string }
	wanted := []item{}
	for owner := island.Owner; owner != 0; {
		if owner < 1 || owner > len(value.Owners) {
			return fmt.Errorf("invalid merge owner")
		}
		frame := value.Owners[owner-1]
		if frame.Kind == "keyed" {
			wanted = append(wanted, item{frame.ID, frame.Tag})
		}
		owner = frame.Parent
	}
	actual := []item{}
	for node := root.Get("parentElement"); !node.IsNull() && !node.IsUndefined() && !node.Equal(stop); node = node.Get("parentElement") {
		if node.Call("hasAttribute", "data-nextgo-key").Bool() {
			actual = append(actual, item{node.Call("getAttribute", "data-nextgo-key").String(), node.Get("localName").String()})
		}
	}
	if len(actual) != len(wanted) {
		return fmt.Errorf("merge keyed ancestry changed")
	}
	for i := range actual {
		if actual[i] != wanted[i] {
			return fmt.Errorf("merge keyed ancestry differs")
		}
	}
	return nil
}
func mergeClientTreeSafe(root js.Value) bool {
	if _, err := mergeBoundedNodes(root, retainedViewNodes); err != nil {
		return false
	}
	nodes := root.Call("querySelectorAll", "*")
	if nodes.Length() > retainedViewNodes {
		return false
	}
	for i := -1; i < nodes.Length(); i++ {
		node := root
		if i >= 0 {
			node = nodes.Index(i)
		}
		tag := strings.ToLower(node.Get("localName").String())
		if strings.Contains(tag, "-") || node.Get("namespaceURI").String() != "http://www.w3.org/1999/xhtml" {
			return false
		}
		for _, name := range []string{"is", "id", "form", "for", "autofocus", "list", "headers", "usemap", "popovertarget", "aria-labelledby", "aria-describedby", "aria-controls", "aria-owns"} {
			if node.Call("hasAttribute", name).Bool() {
				return false
			}
		}
		switch tag {
		case "form", "dialog", "video", "audio", "canvas", "iframe", "object", "embed", "script", "style", "template", "link", "meta", "base", "map", "area":
			return false
		}
		if (tag == "input" && strings.EqualFold(node.Call("getAttribute", "type").String(), "radio")) || (tag == "details" && node.Call("hasAttribute", "name").Bool()) || (tag == "a" && node.Call("hasAttribute", "name").Bool()) || node.Call("hasAttribute", "shadowrootmode").Bool() {
			return false
		}
		shadow := node.Get("shadowRoot")
		if shadow.Type() == js.TypeObject && !shadow.IsNull() {
			return false
		}
	}
	return true
}
func mergeDocumentFromDOM(value *navigation.MergeDescriptor, root js.Value, revision uint64, rawHTML string, baseNodes int) (*mergeDocument, error) {
	if value == nil || value.ValidateDescriptor() != nil || root.IsNull() || root.IsUndefined() {
		return nil, fmt.Errorf("invalid merge document")
	}
	if root.Get("localName").String() == "body" && root.Get("attributes").Length() != 0 {
		return nil, errMergeUnsupported
	}
	count, countErr := mergeBoundedNodes(root, retainedViewNodes)
	if countErr != nil {
		return nil, errMergeUnsupported
	}
	if baseNodes > retainedTotalNodes-count {
		return nil, errRetainedCapacity
	}
	nodes := root.Call("querySelectorAll", "["+navigation.MergeNodeAttribute+"]")
	if nodes.Length() != len(value.Islands) {
		return nil, fmt.Errorf("merge marker count differs")
	}
	byNode := map[int]js.Value{}
	for i := 0; i < nodes.Length(); i++ {
		node := nodes.Index(i)
		raw := node.Call("getAttribute", navigation.MergeNodeAttribute).String()
		ordinal, err := strconv.Atoi(raw)
		if err != nil || strconv.Itoa(ordinal) != raw || ordinal < 1 || ordinal > len(value.Islands) {
			return nil, fmt.Errorf("invalid merge marker")
		}
		if _, exists := byNode[ordinal]; exists {
			return nil, fmt.Errorf("duplicate merge marker")
		}
		byNode[ordinal] = node
	}
	cloneCharge := baseNodes + count
	for _, island := range value.Islands {
		node, ok := byNode[island.Node]
		if !ok {
			return nil, fmt.Errorf("missing merge marker")
		}
		n, err := mergeBoundedNodes(node, retainedViewNodes)
		if err != nil {
			return nil, err
		}
		cloneCharge += n + 4
		if cloneCharge > retainedTotalNodes {
			return nil, errRetainedCapacity
		}
	}
	current := &mergeDocument{descriptor: value, boundaries: map[string]*mergeBoundary{}, revision: revision, privacy: mergePrivacy}
	for _, island := range value.Islands {
		node, exists := byNode[island.Node]
		if !exists || node.Get("localName").String() != "div" || node.Call("getAttribute", "data-nextgo-client").String() != "1" || node.Call("getAttribute", "data-nextgo-island").String() != island.Component {
			return nil, fmt.Errorf("merge component marker differs")
		}
		if !mergeClientTreeSafe(node) {
			return nil, errMergeUnsupported
		}
		props := node.Call("getAttribute", "data-nextgo-props")
		if props.IsNull() || mergeHash(props.String()) != island.PropsSHA256 {
			return nil, fmt.Errorf("merge props marker differs")
		}
		if err := mergeKeyAncestry(value, island, node, root); err != nil {
			return nil, err
		}
		address, err := value.Address(island)
		if err != nil {
			return nil, err
		}
		fallback, fallbackErr := mergeRangeFallback(value, island, node, rawHTML)
		if fallbackErr != nil {
			return nil, fallbackErr
		}
		current.boundaries[address] = &mergeBoundary{island: island, address: address, root: node, fallback: fallback, valid: false}
	}
	return current, nil
}

// Server-owned carriers and gaps must not synchronously invoke application code
// or change document-wide native control associations during this first plan.
func mergeColdBoundarySafe(root js.Value) error {
	if _, err := mergeBoundedNodes(root, retainedViewNodes); err != nil {
		return err
	}
	if lazyModules[root.Call("getAttribute", "data-nextgo-island").String()] != nil {
		return errMergeUnsupported
	}
	ids := map[string]int{}
	all := root.Call("querySelectorAll", "*")
	for i := -1; i < all.Length(); i++ {
		node := root
		if i >= 0 {
			node = all.Index(i)
		}
		tag := strings.ToLower(node.Get("localName").String())
		if strings.Contains(tag, "-") || node.Get("namespaceURI").String() != "http://www.w3.org/1999/xhtml" {
			return errMergeUnsupported
		}
		switch tag {
		case "dialog", "video", "audio", "canvas", "iframe", "object", "embed", "script", "style", "template", "link", "meta", "base", "map", "area":
			return errMergeUnsupported
		}
		for _, attr := range []string{"is", "form", "autofocus", "shadowrootmode"} {
			if node.Call("hasAttribute", attr).Bool() {
				return errMergeUnsupported
			}
		}
		if (tag == "details" || tag == "a" || tag == "form") && node.Call("hasAttribute", "name").Bool() {
			return errMergeUnsupported
		}
		shadow := node.Get("shadowRoot")
		if shadow.Type() == js.TypeObject && !shadow.IsNull() {
			return errMergeUnsupported
		}
		if node.Call("hasAttribute", "id").Bool() {
			id := node.Call("getAttribute", "id").String()
			ids[id]++
			if ids[id] != 1 || strings.HasPrefix(id, "nextgo-") {
				return errMergeUnsupported
			}
		}
		attrs := ui.Attrs{}
		raw := node.Get("attributes")
		for j := 0; j < raw.Length(); j++ {
			attr := raw.Index(j)
			attrs[attr.Get("name").String()] = attr.Get("value").String()
		}
		if err := ui.RenderTo(context.Background(), discardKeyed{}, ui.El(tag, attrs)); err != nil {
			return errMergeUnsupported
		}
	}
	for i := -1; i < all.Length(); i++ {
		node := root
		if i >= 0 {
			node = all.Index(i)
		}
		if node.Get("localName").String() == "label" && node.Call("hasAttribute", "for").Bool() {
			if ids[node.Call("getAttribute", "for").String()] != 1 {
				return errMergeUnsupported
			}
		}
	}
	return nil
}
func validateMergeSkeleton(root js.Value, holes map[string]js.Value) error {
	if _, err := mergeBoundedNodes(root, retainedViewNodes); err != nil {
		return err
	}
	ids := root.Call("querySelectorAll", "[id]")
	seenIDs := map[string]bool{}
	for i := 0; i < ids.Length(); i++ {
		id := ids.Index(i).Call("getAttribute", "id").String()
		if id == "" || seenIDs[id] || strings.HasPrefix(id, "nextgo-") {
			return errMergeUnsupported
		}
		seenIDs[id] = true
	}
	body := doc.Get("body")
	if _, err := mergeBoundedNodes(body, retainedTotalNodes); err != nil {
		return errMergeUnsupported
	}
	existing := body.Call("querySelectorAll", "[id]")
	app := doc.Call("getElementById", "nextgo-app")
	for i := 0; i < existing.Length(); i++ {
		node := existing.Index(i)
		if !app.Call("contains", node).Bool() && seenIDs[node.Call("getAttribute", "id").String()] {
			return errMergeUnsupported
		}
	}
	pending := []js.Value{root}
	count := 0
	for len(pending) > 0 {
		node := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		count++
		if count > retainedTotalNodes {
			return fmt.Errorf("merge node capacity exceeded")
		}
		if mergeByRoot(node, holes) != "" {
			continue
		}
		if node.Get("nodeType").Int() == 1 && node.Call("hasAttribute", "data-nextgo-island").Bool() {
			if err := mergeColdBoundarySafe(node); err != nil {
				return err
			}
			continue
		}
		switch node.Get("nodeType").Int() {
		case 3, 8:
			continue
		case 1:
			tag := strings.ToLower(node.Get("localName").String())
			if node.Get("namespaceURI").String() != "http://www.w3.org/1999/xhtml" {
				return fmt.Errorf("merge namespace is unsupported")
			}
			if node.Equal(root) {
				if node.Call("hasAttribute", "is").Bool() {
					return errMergeUnsupported
				}
				shadow := node.Get("shadowRoot")
				if shadow.Type() == js.TypeObject && !shadow.IsNull() {
					return errMergeUnsupported
				}
				break
			}
			if strings.Contains(tag, "-") {
				if tag != "nextgo-layout" && tag != "nextgo-outlet" {
					return fmt.Errorf("custom merge carrier is unsupported")
				}
				custom := win.Get("customElements")
				if !custom.IsUndefined() && !custom.IsNull() && custom.Get("get").Type() == js.TypeFunction && custom.Call("get", tag).Type() == js.TypeFunction {
					return fmt.Errorf("custom framework carrier is unsupported")
				}
			}
			switch tag {
			case "form", "label", "datalist", "dialog", "video", "audio", "canvas", "iframe", "object", "embed", "script", "style", "template", "link", "meta", "base", "map", "area":
				return fmt.Errorf("server merge control or reactive element is unsupported")
			}
			shadow := node.Get("shadowRoot")
			if shadow.Type() == js.TypeObject && !shadow.IsNull() {
				return errMergeUnsupported
			}
			if node.Call("hasAttribute", "is").Bool() || node.Call("hasAttribute", "id").Bool() || (tag == "input" && strings.EqualFold(node.Call("getAttribute", "type").String(), "radio")) || node.Call("hasAttribute", "autofocus").Bool() || node.Call("hasAttribute", "form").Bool() || node.Call("hasAttribute", "shadowrootmode").Bool() || (tag == "details" && node.Call("hasAttribute", "name").Bool()) || (tag == "a" && node.Call("hasAttribute", "name").Bool()) {
				return fmt.Errorf("merge native ownership is unsupported")
			}
			attrs := ui.Attrs{}
			raw := node.Get("attributes")
			for i := 0; i < raw.Length(); i++ {
				attr := raw.Index(i)
				name := attr.Get("name").String()
				attrs[name] = attr.Get("value").String()
			}
			if err := ui.RenderTo(context.Background(), discardKeyed{}, ui.El(tag, attrs)); err != nil {
				return fmt.Errorf("unsafe merge carrier: %w", err)
			}
		default:
			return fmt.Errorf("unsupported merge node")
		}
		children := mergeChildren(node)
		pending = append(pending, children...)
	}
	return nil
}
func mergeNodeCharge(root js.Value) int {
	count, err := mergeBoundedNodes(root, retainedTotalNodes)
	if err != nil {
		return retainedTotalNodes + 1
	}
	return count
}
func mergeHoleList(node js.Value, roots map[string]js.Value) []string {
	if key := mergeByRoot(node, roots); key != "" {
		return []string{key}
	}
	out := []string{}
	for _, child := range mergeChildren(node) {
		out = append(out, mergeHoleList(child, roots)...)
	}
	return out
}
func mergeSignature(keys []string) string {
	var out strings.Builder
	for _, key := range keys {
		out.WriteString(strconv.Itoa(len(key)))
		out.WriteByte(':')
		out.WriteString(key)
	}
	return out.String()
}

type mergeAnchor struct {
	node      js.Value
	keys      []string
	signature string
}

func mergeAnchors(parent js.Value, roots map[string]js.Value) []mergeAnchor {
	anchors := []mergeAnchor{}
	for _, child := range mergeChildren(parent) {
		keys := mergeHoleList(child, roots)
		if len(keys) > 0 {
			anchors = append(anchors, mergeAnchor{child, keys, mergeSignature(keys)})
		}
	}
	return anchors
}

// Only a common stationary subsequence survives. No insertBefore/moveBefore is
// used to claim that a moved live input kept its native state.
func mergeCommon(a, b []mergeAnchor) map[string]bool {
	left, right := make([]string, len(a)), make([]string, len(b))
	for i := range a {
		left[i] = a[i].signature
	}
	for i := range b {
		right[i] = b[i].signature
	}
	keep, err := mergeStableSubsequence(left, right)
	if err != nil {
		return map[string]bool{}
	}
	return keep
}
func mergePruneStationary(old, fresh js.Value, oldRoots, newRoots map[string]js.Value, keep map[string]bool) {
	a, b := mergeAnchors(old, oldRoots), mergeAnchors(fresh, newRoots)
	common := mergeCommon(a, b)
	paired := map[string]mergeAnchor{}
	for _, item := range b {
		paired[item.signature] = item
	}
	for _, left := range a {
		right, ok := paired[left.signature]
		if !ok || !common[left.signature] {
			for _, key := range left.keys {
				delete(keep, key)
			}
			continue
		}
		if mergeByRoot(left.node, oldRoots) != "" && mergeByRoot(right.node, newRoots) != "" {
			continue
		}
		if left.node.Get("nodeType").Int() != 1 || right.node.Get("nodeType").Int() != 1 || left.node.Get("localName").String() != right.node.Get("localName").String() || left.node.Get("namespaceURI").String() != right.node.Get("namespaceURI").String() {
			for _, key := range left.keys {
				delete(keep, key)
			}
			continue
		}
		mergePruneStationary(left.node, right.node, oldRoots, newRoots, keep)
	}
}
func buildMergeCarrier(old, fresh js.Value, oldRoots, newRoots map[string]js.Value, boundaries map[string]*mergeBoundary) (*mergeCarrier, error) {
	result := &mergeCarrier{old: old, fresh: fresh}
	if key := mergeByRoot(old, oldRoots); key != "" {
		if key != mergeByRoot(fresh, newRoots) {
			return nil, fmt.Errorf("merge frontier mismatch")
		}
		result.rootBoundary = boundaries[key]
		return result, nil
	}
	a, b := mergeAnchors(old, oldRoots), mergeAnchors(fresh, newRoots)
	if len(a) != len(b) {
		return nil, fmt.Errorf("merge anchor count changed")
	}
	for i := range a {
		if a[i].signature != b[i].signature {
			return nil, fmt.Errorf("merge anchor order changed")
		}
		child, err := buildMergeCarrier(a[i].node, b[i].node, oldRoots, newRoots, boundaries)
		if err != nil {
			return nil, err
		}
		result.anchors = append(result.anchors, child)
	}
	return result, nil
}
func mergeAttrs(old, fresh js.Value) {
	attrs := fresh.Get("attributes")
	wanted := map[string]string{}
	for i := 0; i < attrs.Length(); i++ {
		attr := attrs.Index(i)
		wanted[attr.Get("name").String()] = attr.Get("value").String()
	}
	previous := old.Get("attributes")
	remove := []string{}
	for i := 0; i < previous.Length(); i++ {
		name := previous.Index(i).Get("name").String()
		if name == "data-nextgo-hydrated" {
			continue
		}
		if _, ok := wanted[name]; !ok {
			remove = append(remove, name)
		}
	}
	for _, name := range remove {
		old.Call("removeAttribute", name)
	}
	for name, value := range wanted {
		if old.Call("getAttribute", name).IsNull() || old.Call("getAttribute", name).String() != value {
			old.Call("setAttribute", name, value)
		}
	}
}
func installMergeCarrier(carrier *mergeCarrier, top bool) {
	if !top {
		mergeAttrs(carrier.old, carrier.fresh)
	}
	if carrier.rootBoundary != nil {
		return
	}
	oldNodes, newNodes := mergeChildren(carrier.old), mergeChildren(carrier.fresh)
	oldAt, newAt := 0, 0
	for index := 0; index <= len(carrier.anchors); index++ {
		var oldAnchor, newAnchor js.Value
		oldEnd, newEnd := len(oldNodes), len(newNodes)
		if index < len(carrier.anchors) {
			oldAnchor, newAnchor = carrier.anchors[index].old, carrier.anchors[index].fresh
			oldEnd = oldAt
			for oldEnd < len(oldNodes) && !oldNodes[oldEnd].Equal(oldAnchor) {
				oldEnd++
			}
			newEnd = newAt
			for newEnd < len(newNodes) && !newNodes[newEnd].Equal(newAnchor) {
				newEnd++
			}
		}
		for _, node := range oldNodes[oldAt:oldEnd] {
			carrier.old.Call("removeChild", node)
		}
		for _, node := range newNodes[newAt:newEnd] {
			if index < len(carrier.anchors) {
				carrier.old.Call("insertBefore", node, oldAnchor)
			} else {
				carrier.old.Call("appendChild", node)
			}
		}
		if index < len(carrier.anchors) {
			installMergeCarrier(carrier.anchors[index], false)
		}
		oldAt, newAt = oldEnd+1, newEnd+1
	}
}

// Unused generated module registrations do not change a page's ownership.
// Actual lazy roots remain cold. Bound the live trees before allocating a
// selector result; this guard is repeated after owned callback readers settle.
func mergeContainsLazyBoundary(root js.Value) bool {
	if len(lazyModules) == 0 {
		return false
	}
	if root.Type() != js.TypeObject || root.IsNull() || root.IsUndefined() {
		return true
	}
	if _, err := mergeBoundedNodes(root, retainedViewNodes); err != nil {
		return true
	}
	if root.Get("querySelectorAll").Type() != js.TypeFunction || root.Get("getAttribute").Type() != js.TypeFunction {
		return true
	}
	nodes := root.Call("querySelectorAll", "[data-nextgo-island]")
	if nodes.Length() > retainedViewNodes {
		return true
	}
	for i := -1; i < nodes.Length(); i++ {
		node := root
		if i >= 0 {
			node = nodes.Index(i)
		}
		name := node.Call("getAttribute", "data-nextgo-island")
		if name.Type() == js.TypeString && lazyModules[name.String()] != nil {
			return true
		}
	}
	return false
}
func prepareMergeDOM(ctx context.Context, old, fresh js.Value, data envelope, target string) (*mergeDOMPlan, error) {
	if data.merge == nil || !mergeRuntimeEnabled() || mergeCurrent == nil || mergeCurrent.revoked || retainedCurrent != nil || mergeContainsLazyBoundary(old) || mergeContainsLazyBoundary(fresh) {
		return nil, nil
	}
	prior := mergeCurrent
	if prior.descriptor.Partition != data.merge.Partition {
		return nil, nil
	}
	revision := propsDocumentRevision() + 1
	if revision == 0 {
		return nil, fmt.Errorf("merge revision exhausted")
	}
	oldCount, oldErr := mergeBoundedNodes(old, retainedViewNodes)
	if oldErr != nil {
		return nil, nil
	}
	baseNodes := oldCount
	for _, boundary := range prior.boundaries {
		if !mergeClientTreeSafe(boundary.root) {
			return nil, nil
		}
		baseNodes += mergeNodeCharge(boundary.fallback)
	}
	for _, entry := range retainedOwners.entries {
		baseNodes += entry.Nodes
	}
	next, err := mergeDocumentFromDOM(data.merge, fresh, revision, data.HTML, baseNodes)
	if errors.Is(err, errMergeUnsupported) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	transferred := false
	var discarded []*preparedProps
	defer func() {
		if !transferred {
			for _, ticket := range discarded {
				ticket.Close()
			}
		}
	}()
	fence := newMergePreparationFence(ctx, prior, navigationID, cacheEpoch, mergePrivacy)
	plan := &mergeDOMPlan{previous: prior, next: next, parent: old.Get("parentNode"), candidates: map[string]*mergeBoundary{}, freshRoots: map[string]js.Value{}, beforeID: navigationID, beforeEpoch: cacheEpoch, privacy: mergePrivacy, target: target, fence: fence}
	pre := fence.valid
	oldRoots, newRoots := map[string]js.Value{}, map[string]js.Value{}
	for key, incoming := range next.boundaries {
		previous := prior.boundaries[key]
		if previous == nil || !previous.valid || previous.scope == nil || !previous.scope.mergeClientReady() || previous.instance != previous.scope.mergeInstance() || !previous.root.Get("isConnected").Bool() {
			continue
		}
		if lazyModules[incoming.island.Component] != nil {
			continue
		}
		parent := incoming.root.Get("parentElement")
		if !parent.IsNull() && !parent.Call("closest", "[data-nextgo-island]").IsNull() {
			continue
		}
		plan.candidates[key] = previous
		oldRoots[key] = previous.root
		newRoots[key] = incoming.root
	}
	// All non-opted ordinary islands stay cold. Their native controls belong to
	// those cold boundaries rather than to retained server carrier state.
	oldHoles, newHoles := map[string]js.Value{}, map[string]js.Value{}
	for key, boundary := range prior.boundaries {
		oldHoles[key] = boundary.root
	}
	for key, boundary := range next.boundaries {
		newHoles[key] = boundary.root
	}
	if err := validateMergeSkeleton(old, oldHoles); err != nil {
		return nil, nil
	}
	if err := validateMergeSkeleton(fresh, newHoles); err != nil {
		return nil, nil
	}
	keep := map[string]bool{}
	for key := range plan.candidates {
		keep[key] = true
	}
	for turns := 0; turns <= navigation.MaxMergeIslands; turns++ {
		count := len(keep)
		mergePruneStationary(old, fresh, oldRoots, newRoots, keep)
		for key := range oldRoots {
			if !keep[key] {
				delete(oldRoots, key)
				delete(newRoots, key)
				delete(plan.candidates, key)
			}
		}
		if len(keep) == count {
			break
		}
		if turns == navigation.MaxMergeIslands {
			return nil, fmt.Errorf("merge planner did not converge")
		}
	}
	// Each retained source prepares once. A failed source becomes its exact fresh
	// cold boundary, without forcing unrelated stationary siblings to reset.
	scopes := []*Scope{}
	for _, entry := range data.merge.Islands {
		key, addressErr := data.merge.Address(entry)
		if addressErr != nil {
			return nil, addressErr
		}
		previous := plan.candidates[key]
		if previous == nil {
			continue
		}
		incoming := next.boundaries[key]
		prepared, prepErr := prepareScopeProps(ctx, previous.scope, []byte(incoming.root.Call("getAttribute", "data-nextgo-props").String()), mergeLocation(target), revision, pre)
		if !pre() {
			if prepared != nil {
				prepared.Close()
			}
			return nil, context.Canceled
		}
		if errors.Is(prepErr, errPropsCapacity) {
			return nil, prepErr
		}
		if prepErr != nil {
			delete(plan.candidates, key)
			delete(oldRoots, key)
			delete(newRoots, key)
			continue
		}
		plan.prepared = append(plan.prepared, prepared)
		discarded = append(discarded, prepared)
		scopes = append(scopes, previous.scope)
	}
	plan.tree, err = buildMergeCarrier(old, fresh, oldRoots, newRoots, plan.candidates)
	if err != nil {
		return nil, err
	}
	for key, previous := range plan.candidates {
		incoming := next.boundaries[key]
		plan.freshRoots[key] = incoming.root
		incoming.root = previous.root
		incoming.scope = previous.scope
		incoming.instance = previous.instance
		incoming.valid = true
	}
	staticBytes, budgetErr := mergePlanBudget(plan, data)
	if budgetErr != nil {
		return nil, budgetErr
	}
	for _, scope := range mounted {
		inside := old.Call("contains", scope.Root.Value).Bool()
		retained := false
		for _, boundary := range plan.candidates {
			if boundary.scope == scope {
				retained = true
				break
			}
		}
		if inside && !retained {
			plan.retiring = append(plan.retiring, scope)
		}
	}
	// Cohort capture is deferred until the caller has completed detached head
	// preparation and quiesced owned readers. No authored work occurs between
	// that capture and the callback-free live install.
	plan.scopes = scopes
	plan.staticBytes = staticBytes
	plan.next.scopes = map[*Scope]struct{}{}
	for _, scope := range scopes {
		plan.next.scopes[scope] = struct{}{}
	}
	transferred = true
	return plan, nil
}

// Recount the current old tree rather than trusting a pre-reader snapshot.
// These are bounded policy estimates of framework-owned references, not RSS.
func mergePlanBudget(plan *mergeDOMPlan, data envelope) (int, error) {
	old, fresh := plan.tree.old, plan.tree.fresh
	nodes := mergeNodeCharge(old) + mergeNodeCharge(fresh)
	if nodes > retainedTotalNodes {
		return 0, errRetainedCapacity
	}
	bytes := int(envelopeBytes(data)) + len(old.Get("innerHTML").String()) + len(doc.Get("head").Get("innerHTML").String()) + nodes*96
	for key, boundary := range plan.next.boundaries {
		incoming := boundary.root
		if root, ok := plan.freshRoots[key]; ok {
			incoming = root
		}
		bytes += 3 * len(incoming.Call("getAttribute", "data-nextgo-props").String())
		nodes += mergeNodeCharge(boundary.fallback)
		bytes += boundary.island.End - boundary.island.Start
	}
	for _, boundary := range plan.previous.boundaries {
		nodes += mergeNodeCharge(boundary.fallback)
		bytes += boundary.island.End - boundary.island.Start
	}
	for _, entry := range retainedOwners.entries {
		nodes += entry.Nodes
		bytes += entry.Bytes
	}
	if nodes > retainedTotalNodes || bytes+propsRegisteredBytes+propsWorkerByteLimit+propsTransientBytes() > retainedMetadataBytes {
		return 0, errRetainedCapacity
	}
	return bytes, nil
}
func mergeCarrierStillExact(carrier *mergeCarrier, oldRoots, newRoots map[string]js.Value) bool {
	if carrier.rootBoundary != nil {
		return mergeByRoot(carrier.old, oldRoots) != "" && mergeByRoot(carrier.old, oldRoots) == mergeByRoot(carrier.fresh, newRoots)
	}
	a, b := mergeAnchors(carrier.old, oldRoots), mergeAnchors(carrier.fresh, newRoots)
	if len(a) != len(carrier.anchors) || len(b) != len(carrier.anchors) {
		return false
	}
	for i, child := range carrier.anchors {
		if a[i].signature != b[i].signature || !a[i].node.Equal(child.old) || !b[i].node.Equal(child.fresh) || !child.old.Get("parentNode").Equal(carrier.old) || !child.fresh.Get("parentNode").Equal(carrier.fresh) || child.old.Get("localName").String() != child.fresh.Get("localName").String() || child.old.Get("namespaceURI").String() != child.fresh.Get("namespaceURI").String() {
			return false
		}
		if !mergeCarrierStillExact(child, oldRoots, newRoots) {
			return false
		}
	}
	return true
}

// A reader may finish by changing its owned DOM. Revalidate after it settles;
// never install an earlier plan over changed native ownership or anchor shape.
func validatePreparedMergePlan(plan *mergeDOMPlan, data envelope) error {
	if !plan.tree.old.Get("isConnected").Bool() || !doc.Call("getElementById", "nextgo-app").Equal(plan.tree.old) || !plan.tree.old.Get("parentNode").Equal(plan.parent) {
		return errMergeOwnership
	}
	if mergeContainsLazyBoundary(plan.tree.old) || mergeContainsLazyBoundary(plan.tree.fresh) {
		return errMergeUnsupported
	}
	if _, err := mergeBoundedNodes(plan.tree.old, retainedViewNodes); err != nil {
		return err
	}
	oldHoles, newHoles := map[string]js.Value{}, map[string]js.Value{}
	oldRoots, newRoots := map[string]js.Value{}, map[string]js.Value{}
	for key, boundary := range plan.previous.boundaries {
		if !mergeClientTreeSafe(boundary.root) {
			return errMergeUnsupported
		}
		oldHoles[key] = boundary.root
	}
	for key, boundary := range plan.next.boundaries {
		incoming := boundary.root
		if fresh, ok := plan.freshRoots[key]; ok {
			incoming = fresh
		}
		newHoles[key] = incoming
	}
	for key, boundary := range plan.candidates {
		if !boundary.valid || boundary.scope == nil || !boundary.scope.mergeClientReady() || boundary.scope.mergeInstance() != boundary.instance || boundary.root.Call("getAttribute", "data-nextgo-island").String() != boundary.island.Component {
			return errMergeUnsupported
		}
		oldRoots[key] = boundary.root
		newRoots[key] = plan.freshRoots[key]
	}
	if err := validateMergeSkeleton(plan.tree.old, oldHoles); err != nil {
		return errMergeUnsupported
	}
	if err := validateMergeSkeleton(plan.tree.fresh, newHoles); err != nil {
		return errMergeUnsupported
	}
	if !mergeCarrierStillExact(plan.tree, oldRoots, newRoots) {
		return errMergeUnsupported
	}
	// Retiring roots may also have changed while a retained callback was active.
	// A newly mounted ownership tree must not escape captured terminal disposal.
	for _, scope := range plan.retiring {
		if scope.closed || !plan.tree.old.Call("contains", scope.Root.Value).Bool() {
			return errMergeUnsupported
		}
	}
	for _, scope := range mounted {
		if !plan.tree.old.Call("contains", scope.Root.Value).Bool() {
			continue
		}
		known := false
		for _, kept := range plan.scopes {
			if kept == scope {
				known = true
				break
			}
		}
		if !known {
			for _, retiring := range plan.retiring {
				if retiring == scope {
					known = true
					break
				}
			}
		}
		if !known {
			return errMergeUnsupported
		}
	}
	bytes, err := mergePlanBudget(plan, data)
	if err != nil {
		return err
	}
	plan.staticBytes = bytes
	return nil
}
