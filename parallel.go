package nextgo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/routing"
	"github.com/Suhaibinator/Next.go/routeinfo"
	"github.com/Suhaibinator/Next.go/ui"
)

// PrimarySlot identifies the implicit children branch of a parallel layout.
// Fallback registrations exist only to compose URLs supplied by named slots.
// A fallback Page is the owner's Default or a function returning ErrNotFound.
type PrimarySlot struct {
	ID, Base string
	Layout   int
	Fallback bool
}

// Slot declares a named parallel branch owned by one layout wrapper. Generated
// slot_name folders supply these registrations. ID is a stable owner/name key;
// Base is the owner's logical URL pattern and Layout its Route.Layouts index.
// Routes use full logical patterns, with only the slot's own layout chain.
type Slot struct {
	ID, Name, Base string
	Layout         int
	Routes         []Route
	// Intercepts are absolute target pages rendered only during soft navigation
	// from this slot's active primary owner. Each target also needs a normal page.
	Intercepts []Route
	// Default is a function-only shorthand. Choose DefaultRoute to include
	// slot-local layouts, templates, metadata and boundaries.
	Default      PageFunc
	DefaultRoute *Route
}

var ErrSlotScope = errors.New("nextgo: Slot requires its declared layout scope")
var ErrSlotResponse = errors.New("nextgo: parallel branches cannot mutate the response; use the primary page or middleware")

type parallelRequestKey struct{}
type primaryFrame struct {
	primary           PrimarySlot
	current, selected *Context
	key, parent, url  string
	retained          bool
	defaulted         bool
}
type parallelState struct {
	app      *App
	route    Route
	carrier  navigation.SlotState
	owner    int
	nodes    map[string]ui.Node
	status   int
	current  *Context
	frames   []primaryFrame
	pool     []Route
	fallback *Route
	leaf     *Context
	infos    map[string]routeinfo.Selection
}

func hasPrimaries(r Route) bool { return r.Primary != nil || len(r.Primaries) > 0 }
func primaryChain(r Route) []PrimarySlot {
	if r.Primary != nil {
		return []PrimarySlot{*r.Primary}
	}
	return r.Primaries
}
func primaryFor(r Route, id string) (PrimarySlot, bool) {
	for _, p := range primaryChain(r) {
		if p.ID == id {
			return p, true
		}
	}
	return PrimarySlot{}, false
}

func slotOwnerPrefix(base, path string) (string, bool) {
	if strings.Contains(base, "*") {
		_, ok := routing.Match(base, path)
		return path, ok
	}
	depth := 0
	if base != "/" {
		depth = len(strings.Split(strings.TrimPrefix(base, "/"), "/"))
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if path == "/" {
		parts = nil
	}
	if len(parts) < depth {
		return "", false
	}
	prefix := "/" + strings.Join(parts[:depth], "/")
	_, ok := routing.Match(base, prefix)
	return prefix, ok
}
func slotScope(slot Slot, path string) (string, bool) { return scopedSlot(slot, path, "") }
func scopedSlot(slot Slot, path, parent string) (string, bool) {
	prefix, ok := slotOwnerPrefix(slot.Base, path)
	if !ok {
		return "", false
	}
	identity := slot.ID + "\x00" + prefix
	if parent != "" {
		identity += "\x00" + parent
	}
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:]), true
}
func (a *App) beginParallel(c *Context, r Route) error {
	if c.slotTarget == nil {
		c.slotTarget = c.Request.URL
	}
	state := parallelState{app: a, route: r, owner: -1, nodes: map[string]ui.Node{}, status: 200, current: c, leaf: c, infos: map[string]routeinfo.Selection{}}
	state.pool = c.routePool
	if state.pool == nil && c.slotParent == "" {
		state.pool = a.routes
	}
	state.fallback = c.routeFallback
	if c.Request.Header.Get("X-Nextgo-Navigate") == "1" {
		if values := c.Request.Header.Values(navigation.SlotsHeader); len(values) > 1 {
			return fmt.Errorf("multiple slot states")
		} else if len(values) == 1 {
			carrier, err := navigation.ParseSlotState(values[0])
			if err != nil {
				return err
			}
			if carrier.Configuration != a.configurationID {
				return fmt.Errorf("incompatible slot state")
			}
			state.carrier = carrier
		}
	}
	c.parallel = &state
	return nil
}

// selectPrimary treats a carrier as a URL selection, then re-runs the selected
// registered page with current request headers. It never trusts cached markup.
func (a *App) selectPrimary(c *Context, r Route) (*Context, Route) {
	if !hasPrimaries(r) {
		return c, r
	}
	state := c.parallel
	selected := c
	parent := c.slotParent
	inherited := c.slotRetained
	last := -1
	for {
		var p PrimarySlot
		found := false
		for _, candidate := range primaryChain(r) {
			if candidate.Layout > last {
				p = candidate
				found = true
				break
			}
		}
		if !found {
			break
		}
		key, ok := scopedSlot(Slot{ID: p.ID, Base: p.Base}, selected.Request.URL.EscapedPath(), parent)
		if !ok {
			break
		} // Registration validation confines every owner to its route.
		frame := primaryFrame{primary: p, current: selected, selected: selected, key: key, parent: parent, url: selected.Request.URL.RequestURI(), retained: inherited, defaulted: p.Fallback}
		if !p.Fallback {
			frame.retained = frame.retained || state.carrier.Selections[key] == frame.url
		} else if previous := state.carrier.Selections[key]; previous != "" {
			candidate, err := url.Parse(previous)
			if err == nil {
				scope, within := scopedSlot(Slot{ID: p.ID, Base: p.Base}, candidate.EscapedPath(), parent)
				if within && scope == key {
					candidates := state.pool
					if state.fallback != nil {
						candidates = append(append([]Route(nil), candidates...), *state.fallback)
					}
					for _, branch := range candidates {
						owner, eligible := primaryFor(branch, p.ID)
						if !eligible || branch.API != nil {
							continue
						}
						params, match := routing.Match(branch.Pattern, candidate.EscapedPath())
						if !match && state.fallback != nil && branch.Pattern == state.fallback.Pattern && owner.Fallback {
							prefix, contains := slotOwnerPrefix(owner.Base, candidate.EscapedPath())
							if contains {
								params, match = routing.Match(owner.Base, prefix)
							}
						}
						if !match {
							continue
						}
						request := selected.Request.Clone(selected.Context)
						request.URL = candidate
						view := &Context{Context: selected.Context, Request: request, Params: params, Query: candidate.Query(), parallel: state, responseRef: selected.responseControls(), slotParent: c.slotParent, routePool: c.routePool, routeFallback: c.routeFallback, slotTarget: c.slotTarget, slotRetained: c.slotRetained, slotDefault: c.slotDefault, interceptSlot: c.interceptSlot, interceptFrom: c.interceptFrom}
						selected = view
						r = branch
						frame.selected = view
						frame.url = candidate.RequestURI()
						frame.retained = true
						frame.defaulted = owner.Fallback
						break
					}
				}
			}
		}
		state.frames = append(state.frames, frame)
		parent = key
		inherited = frame.retained
		last = p.Layout
	}
	state.route = r
	state.leaf = selected
	return selected, r
}
func (c *Context) disablePrimaryRetention() {
	if c.parallel != nil {
		for i := range c.parallel.frames {
			c.parallel.frames[i].retained = false
		}
	}
}
func (state *parallelState) layoutContext(index int) *Context {
	c := state.current
	parent := c.slotParent
	for _, frame := range state.frames {
		if frame.primary.Layout >= index {
			break
		}
		c = frame.selected
		parent = frame.key
	}
	return c.slotView(parent)
}
func (c *Context) primaryNodeAt(index int, body ui.Node) (ui.Node, error) {
	for _, frame := range c.parallel.frames {
		if frame.primary.Layout == index {
			selection, err := c.primarySelection(index)
			if err != nil {
				return nil, err
			}
			return renderSelectedSlotNode(c.Context, frame.key, "children", frame.url, frame.retained, frame.parent, body, &selection)
		}
	}
	return body, nil
}

// Slot returns a server-rendered named branch for the currently executing
// layout. Defaults are used on direct URL loads; soft navigation may select a
// previous branch, which is reloaded and authorized with the current request.
// Document metadata belongs to the primary page. Slot errors use local route
// boundaries; response mutations are rejected even if their error is ignored.
func (c *Context) Slot(name string) (ui.Node, error) {
	if c.notFoundLayout {
		return ui.Fragment(), nil
	}
	if c.parallel == nil || c.parallel.owner < 0 {
		return nil, ErrSlotScope
	}
	state := c.parallel
	for _, slot := range state.route.Slots {
		if slot.Name != name || slot.Layout != state.owner {
			continue
		}
		key, valid := scopedSlot(slot, c.Request.URL.EscapedPath(), c.slotParent)
		if !valid {
			return nil, ErrSlotScope
		}
		if node := state.nodes[key]; node != nil {
			return node, nil
		}
		selected := c.Request.URL
		target := c.slotTarget
		if target != nil {
			if scope, ok := scopedSlot(slot, target.EscapedPath(), c.slotParent); ok && scope == key {
				selected = target
			}
		}
		var route *Route
		var params map[string]string
		intercepted := false
		if target != nil && c.interceptSlot == slot.ID {
			for i := range slot.Intercepts {
				branch := &slot.Intercepts[i]
				if values, ok := interceptionParams(slot, c.Request.URL, branch.Pattern, target); ok {
					route, params, selected, intercepted = branch, values, target, true
					break
				}
			}
		}
		selectRoute := func(u *url.URL) {
			for i := range slot.Routes {
				if values, ok := routing.Match(slot.Routes[i].Pattern, u.EscapedPath()); ok {
					route = &slot.Routes[i]
					params = values
					break
				}
			}
		}
		// A retained parent renders at its old URL; matching children must
		// still use the navigation destination, rather than remount old matches.
		if !intercepted && (target == nil || selected == target) {
			selectRoute(selected)
		}
		defaulted := false
		retained := route != nil && state.carrier.Selections[key] == selected.RequestURI()
		if route == nil {
			if previous := state.carrier.Selections[key]; previous != "" {
				candidate, _ := url.Parse(previous)
				if scope, ok := scopedSlot(slot, candidate.EscapedPath(), c.slotParent); ok && scope == key {
					selectRoute(candidate)
					if route == nil && slot.fallback() != nil {
						route = slot.fallback()
						defaulted = true
						prefix, _ := slotOwnerPrefix(slot.Base, candidate.EscapedPath())
						params, _ = routing.Match(slot.Base, prefix)
					}
					if route != nil {
						selected = candidate
						retained = true
					}
				}
			}
		}
		if route == nil {
			route = slot.fallback()
			defaulted = true
			if route == nil {
				return nil, ErrNotFound
			}
			prefix, _ := slotOwnerPrefix(slot.Base, selected.EscapedPath())
			params, _ = routing.Match(slot.Base, prefix)
		}
		request := c.Request.Clone(c.Context)
		copyURL := *selected
		request.URL = &copyURL
		child := &Context{Context: c.Context, Request: request, Params: params, Query: selected.Query(), slotParent: key, routePool: slot.Routes, routeFallback: slot.fallback(), slotTarget: c.slotTarget, slotRetained: retained, slotDefault: defaulted}
		if intercepted {
			child.interceptFrom = c.Request.URL.RequestURI()
		}
		child.response.readonly = ErrSlotResponse
		page, status := state.app.render(child, *route)
		snapshot := child.response.seal()
		if snapshot.err != nil {
			return nil, snapshot.err
		}
		if status == 401 {
			return nil, ErrUnauthorized
		}
		if status == 403 {
			return nil, ErrForbidden
		}
		if status > state.status {
			state.status = status
		}
		pattern, path := child.parallel.selectionLeaf(slot.Base, defaulted)
		selectionBase := slot.Base
		if intercepted {
			selectionBase = "/"
		}
		segments, err := routeinfo.Segments(pattern, path, selectionBase)
		if err != nil {
			return nil, err
		}
		selection := routeinfo.Selection{Scope: key, Name: name, Segments: segments}
		state.infos[key] = selection
		node, err := renderSelectedSlotNode(c.Context, key, name, selected.RequestURI(), retained && status == 200, c.slotParent, page.Body, &selection)
		if err != nil {
			return nil, err
		}
		state.nodes[key] = node
		return node, nil
	}
	return nil, fmt.Errorf("%w: unknown slot %q", ErrSlotScope, name)
}
func (s Slot) fallback() *Route {
	if s.DefaultRoute != nil {
		return s.DefaultRoute
	}
	if s.Default != nil {
		return &Route{Pattern: s.Base, Page: s.Default}
	}
	return nil
}

// A branch's own hash includes child identities/positions, not child content.
// Child changes can then be patched without resetting the parent's Go state.
type slotRenderKey struct{}
type slotWriter struct {
	html strings.Builder
	hash hash.Hash
	skip int
}

func (w *slotWriter) Write(p []byte) (int, error) {
	if w.skip == 0 {
		if _, err := w.hash.Write(p); err != nil {
			return 0, err
		}
	}
	return w.html.Write(p)
}

type slotNode struct{ html, key string }

func (n slotNode) Render(w io.Writer) error {
	if recorder, ok := ui.RenderContext(w).Value(slotRenderKey{}).(*slotWriter); ok {
		_, _ = io.WriteString(recorder.hash, `<nextgo-slot data-nextgo-slot="`+n.key+`"></nextgo-slot>`)
		recorder.skip++
		defer func() { recorder.skip-- }()
	}
	_, err := io.WriteString(w, n.html)
	return err
}
func (c *Context) slotView(parent string) *Context {
	return &Context{Context: c.Context, Request: c.Request, Params: c.Params, Query: c.Query, parallel: c.parallel, responseRef: c.responseControls(), slotParent: parent, routePool: c.routePool, routeFallback: c.routeFallback, slotTarget: c.slotTarget, slotRetained: c.slotRetained, slotDefault: c.slotDefault, interceptSlot: c.interceptSlot, interceptFrom: c.interceptFrom}
}
func renderSlotNode(ctx context.Context, key, name, selected string, retained bool, parent string, body ui.Node) (ui.Node, error) {
	return renderSelectedSlotNode(ctx, key, name, selected, retained, parent, body, nil)
}
func renderSelectedSlotNode(ctx context.Context, key, name, selected string, retained bool, parent string, body ui.Node, selection *routeinfo.Selection) (ui.Node, error) {
	recorder := &slotWriter{hash: sha256.New()}
	if err := ui.RenderTo(context.WithValue(ctx, slotRenderKey{}, recorder), recorder, body); err != nil {
		return nil, err
	}
	attrs := ui.Attrs{"data-nextgo-slot": key, "data-nextgo-slot-name": name, "data-nextgo-slot-url": selected, "data-nextgo-slot-hash": hex.EncodeToString(recorder.hash.Sum(nil)), "data-nextgo-slot-retain": fmt.Sprint(retained), "data-nextgo-slot-parent": parent}
	if selection != nil {
		raw, err := selectionJSON(*selection)
		if err != nil {
			return nil, err
		}
		attrs["data-nextgo-selection"] = raw
	}
	html, err := ui.HTMLContext(ctx, ui.El("nextgo-slot", attrs, renderedHTML(recorder.html.String())))
	if err != nil {
		return nil, err
	}
	return slotNode{html: html, key: key}, nil
}

func validateSlots(routes []Route) error {
	type shape struct{ count, depth int }
	active := map[*Route]bool{}
	checked := map[*Route]shape{}
	owners := map[string]string{}
	registrations := 0
	var walk func(*Route, int) (shape, error)
	walk = func(r *Route, level int) (shape, error) {
		if level > navigation.MaxSlotDepth {
			return shape{}, fmt.Errorf("parallel slot depth bound exceeded")
		}
		if active[r] {
			return shape{}, fmt.Errorf("cyclic parallel route registration")
		}
		if result, ok := checked[r]; ok {
			return result, nil
		}
		registrations++
		if registrations > navigation.MaxSlotRegistrations {
			return shape{}, fmt.Errorf("parallel route registration bound exceeded")
		}
		active[r] = true
		defer delete(active, r)
		result := shape{count: len(r.Slots)}
		if r.API != nil && (len(r.Slots) > 0 || hasPrimaries(*r)) {
			return result, fmt.Errorf("HTTP endpoints cannot declare parallel slots")
		}
		if r.Primary != nil && len(r.Primaries) > 0 {
			return result, fmt.Errorf("route %s: choose Primary or Primaries", r.Pattern)
		}
		chain := primaryChain(*r)
		previousLayout := -1
		primaryIDs := map[string]bool{}
		for _, p := range chain {
			if p.Layout <= previousLayout || primaryIDs[p.ID] {
				return result, fmt.Errorf("route %s: invalid primary chain order/identity", r.Pattern)
			}
			previousLayout = p.Layout
			primaryIDs[p.ID] = true
			result.count++
			result.depth++
			if r.Cache != nil || p.ID == "" || len(p.ID) > 256 || strings.ContainsAny(p.ID, "\r\n\x00") || p.Layout < 0 || p.Layout >= len(r.Layouts) {
				return result, fmt.Errorf("route %s: invalid primary slot", r.Pattern)
			}
			if _, err := routing.Validate(p.Base); err != nil || strings.Contains(p.Base, "*") {
				return result, fmt.Errorf("route %s: invalid primary slot base", r.Pattern)
			}
			if _, ok := slotOwnerPrefix(p.Base, r.Pattern); !ok {
				return result, fmt.Errorf("route %s: primary slot escapes owner", r.Pattern)
			}
			ids := r.LayoutIDs
			if len(ids) > p.Layout {
				ids = ids[:p.Layout+1]
			}
			ancestorIDs := []string{}
			for _, outer := range chain {
				if outer.Layout >= p.Layout {
					break
				}
				ancestorIDs = append(ancestorIDs, fmt.Sprint(outer.ID, "/", outer.Base, "/", outer.Layout))
			}
			identity := fmt.Sprint(p.Base, "/", p.Layout, "/", ids, "/", ancestorIDs)
			if old, ok := owners[p.ID]; ok && old != identity {
				return result, fmt.Errorf("route %s: inconsistent primary owner identity", r.Pattern)
			}
			owners[p.ID] = identity
		}
		ids, names := map[string]bool{}, map[string]bool{}
		for i := range r.Slots {
			slot := &r.Slots[i]
			if slot.ID == "" || len(slot.ID) > 256 || strings.ContainsAny(slot.ID, "\r\n\x00") || !ruleIdentifier.MatchString(slot.Name) || slot.Name == "children" || slot.Layout < 0 || slot.Layout >= len(r.Layouts) || len(slot.Routes)+len(slot.Intercepts) > 128 {
				return result, fmt.Errorf("route %s: invalid parallel slot registration", r.Pattern)
			}
			if _, ok := slotOwnerPrefix(slot.Base, r.Pattern); !ok {
				return result, fmt.Errorf("route %s: slot owner does not contain route", r.Pattern)
			}
			if _, err := routing.Validate(slot.Base); err != nil || strings.Contains(slot.Base, "*") {
				return result, fmt.Errorf("route %s: invalid parallel owner base", r.Pattern)
			}
			if ids[slot.ID] || names[fmt.Sprint(slot.Layout, "/", slot.Name)] || primaryIDs[slot.ID] {
				return result, fmt.Errorf("route %s: duplicate parallel slot", r.Pattern)
			}
			ids[slot.ID] = true
			names[fmt.Sprint(slot.Layout, "/", slot.Name)] = true
			if slot.Default != nil && slot.DefaultRoute != nil {
				return result, fmt.Errorf("slot %s: choose Default or DefaultRoute", slot.Name)
			}
			if slot.fallback() == nil && len(slot.Routes) == 0 {
				return result, fmt.Errorf("slot %s has no page or default", slot.Name)
			}
			patterns := []string{}
			nested := shape{}
			visit := func(branch *Route) error {
				if len(branch.StaticParamsLayers) > 0 {
					return fmt.Errorf("slot %s: static parameter ancestry is not supported for parallel branches", slot.Name)
				}
				if branch.Prerender != nil {
					return fmt.Errorf("slot %s: prerender is not supported for parallel branches", slot.Name)
				}
				if err := validateSlotBranch(*slot, *branch); err != nil {
					return err
				}
				s, err := walk(branch, level+1)
				if err != nil {
					return err
				}
				nested.count = max(nested.count, s.count)
				nested.depth = max(nested.depth, s.depth)
				return nil
			}
			if slot.DefaultRoute != nil {
				if slot.DefaultRoute.Pattern != slot.Base {
					return result, fmt.Errorf("slot %s: default pattern must equal owner base", slot.Name)
				}
				if err := visit(slot.DefaultRoute); err != nil {
					return result, err
				}
			}
			for j := range slot.Routes {
				branch := &slot.Routes[j]
				if err := visit(branch); err != nil {
					return result, err
				}
				patterns = append(patterns, branch.Pattern)
			}
			interceptPatterns := []string{}
			for j := range slot.Intercepts {
				branch := &slot.Intercepts[j]
				if level != 0 || slot.fallback() == nil {
					return result, fmt.Errorf("slot %s: interceptions require a top-level primary owner and a default", slot.Name)
				}
				if branch.Page == nil || branch.API != nil || branch.Cache != nil || branch.StaticParams != nil {
					return result, fmt.Errorf("slot %s: interceptions require uncached pages without API/static enumeration", slot.Name)
				}
				if err := validateRouteLayers(*branch); err != nil {
					return result, err
				}
				s, err := walk(branch, level+1)
				if err != nil {
					return result, err
				}
				nested.count = max(nested.count, s.count)
				nested.depth = max(nested.depth, s.depth)
				interceptPatterns = append(interceptPatterns, branch.Pattern)
			}
			if err := routing.ValidatePatterns(interceptPatterns); err != nil {
				return result, err
			}
			if err := routing.ValidatePatterns(patterns); err != nil {
				return result, err
			}
			result.count += nested.count
			depth := 1 + nested.depth
			for _, p := range chain {
				if slot.Layout > p.Layout {
					depth++
				}
			}
			result.depth = max(result.depth, depth)
		}
		if result.count > navigation.MaxSlots || result.depth > navigation.MaxSlotDepth {
			return result, fmt.Errorf("route %s exceeds composed parallel slot count/depth bound", r.Pattern)
		}
		checked[r] = result
		return result, nil
	}
	for i := range routes {
		if _, err := walk(&routes[i], 0); err != nil {
			return err
		}
	}
	return nil
}
func validateSlotBranch(slot Slot, branch Route) error {
	if branch.Page == nil || branch.API != nil || branch.StaticParams != nil || branch.Cache != nil {
		return fmt.Errorf("slot %s requires uncached page branches without API/static enumeration", slot.Name)
	}
	if _, err := routing.Validate(branch.Pattern); err != nil {
		return err
	}
	if _, ok := slotOwnerPrefix(slot.Base, branch.Pattern); !ok {
		return fmt.Errorf("slot %s: branch escapes owner base", slot.Name)
	}
	return validateRouteLayers(branch)
}
func freezeSlots(routes []Route) {
	memo := map[*Route]*Route{}
	var clone func(*Route) *Route
	clone = func(source *Route) *Route {
		if saved := memo[source]; saved != nil {
			return saved
		}
		value := *source
		memo[source] = &value
		value.Layouts = append([]LayoutFunc(nil), source.Layouts...)
		value.LayoutIDs = append([]string(nil), source.LayoutIDs...)
		value.LayoutBases = append([]string(nil), source.LayoutBases...)
		value.Metadata = append([]MetadataLayer(nil), source.Metadata...)
		value.Access = append([]AccessBoundary(nil), source.Access...)
		if source.Primary != nil {
			p := *source.Primary
			value.Primary = &p
		}
		value.Primaries = append([]PrimarySlot(nil), source.Primaries...)
		value.Slots = append([]Slot(nil), source.Slots...)
		for i := range value.Slots {
			slot := &value.Slots[i]
			original := &source.Slots[i]
			if original.DefaultRoute != nil {
				slot.DefaultRoute = clone(original.DefaultRoute)
			}
			slot.Routes = make([]Route, len(original.Routes))
			for j := range original.Routes {
				slot.Routes[j] = *clone(&original.Routes[j])
			}
			sort.SliceStable(slot.Routes, func(x, y int) bool { return routing.Less(slot.Routes[x].Pattern, slot.Routes[y].Pattern) })
			slot.Intercepts = make([]Route, len(original.Intercepts))
			for j := range original.Intercepts {
				slot.Intercepts[j] = *clone(&original.Intercepts[j])
			}
			sort.SliceStable(slot.Intercepts, func(x, y int) bool { return routing.Less(slot.Intercepts[x].Pattern, slot.Intercepts[y].Pattern) })
		}
		return &value
	}
	for i := range routes {
		routes[i] = *clone(&routes[i])
	}
}
func (a *App) parallelIdentity() string {
	type declaration struct {
		ID, Name, Base string
		Layout         int
		Default        bool
		Fallback       *int
		Branches       []int
		Intercepts     []int
	}
	type entry struct {
		Pattern   string
		Primaries []PrimarySlot
		Layouts   []string
		Bases     []string
		Slots     []declaration
	}
	entries := []entry{}
	memo := map[*Route]int{}
	var describe func(*Route) int
	describe = func(r *Route) int {
		if index, ok := memo[r]; ok {
			return index
		}
		index := len(entries)
		memo[r] = index
		entries = append(entries, entry{})
		e := entry{Pattern: r.Pattern, Primaries: primaryChain(*r), Layouts: r.LayoutIDs, Bases: r.LayoutBases}
		for i := range r.Slots {
			slot := &r.Slots[i]
			d := declaration{ID: slot.ID, Name: slot.Name, Base: slot.Base, Layout: slot.Layout, Default: slot.fallback() != nil}
			if slot.DefaultRoute != nil {
				value := describe(slot.DefaultRoute)
				d.Fallback = &value
			}
			for j := range slot.Routes {
				d.Branches = append(d.Branches, describe(&slot.Routes[j]))
			}
			for j := range slot.Intercepts {
				d.Intercepts = append(d.Intercepts, describe(&slot.Intercepts[j]))
			}
			e.Slots = append(e.Slots, d)
		}
		entries[index] = e
		return index
	}
	roots := []int{}
	for i := range a.routes {
		r := &a.routes[i]
		if len(r.Slots) > 0 || hasPrimaries(*r) {
			roots = append(roots, describe(r))
		}
	}
	if len(roots) == 0 {
		return ""
	}
	data, _ := json.Marshal(struct {
		Version int
		Roots   []int
		Routes  []entry
	}{5, roots, entries})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
