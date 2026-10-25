//go:build js && wasm

package client

import (
	"fmt"
	"syscall/js"
	"time"
)

const viewportObservedLimit = 128
const viewportQueueLimit = 32
const viewportWorkers = 2
const viewportScanSlice = 256
const viewportURLLimit = 8192

type viewportCandidate struct {
	node  js.Value
	url   string
	epoch uint64
}
type viewportFlight struct {
	key       string
	pending   *prefetchRequest
	candidate viewportCandidate
}
type viewportController struct {
	io, mutations, attempts                   js.Value
	observed                                  []viewportCandidate
	queue                                     []viewportCandidate
	flights                                   [viewportWorkers]viewportFlight
	wake                                      chan struct{}
	recent                                    map[string]uint64
	recentOrder                               []string
	callbacks                                 []js.Func
	listeners                                 []viewportListener
	active, hidden, disposed, scanning, dirty bool
	generation                                uint64
}
type viewportListener struct {
	target   js.Value
	event    string
	callback js.Func
}

var viewportPrefetch *viewportController

// Observer callbacks belong to the document, rather than each page/resume. A
// queued browser callback must never call an already released Go js.Func.
func installViewportPrefetch() {
	if viewportPrefetch != nil || moduleMode || routeManifest.Development || routeManifest.Draft || win.Get("IntersectionObserver").Type() != js.TypeFunction || win.Get("MutationObserver").Type() != js.TypeFunction {
		return
	}
	c := &viewportController{attempts: win.Get("WeakMap").New(), wake: make(chan struct{}, 1)}
	viewportPrefetch = c
	intersect := c.callback(func(args []js.Value) {
		if !c.active || len(args) == 0 {
			return
		}
		entries := args[0]
		for i := 0; i < entries.Length(); i++ {
			entry := entries.Index(i)
			if entry.Get("isIntersecting").Bool() {
				node := entry.Get("target")
				for _, observed := range c.observed {
					if observed.node.Equal(node) {
						c.enqueue(node)
						break
					}
				}
			}
		}
	})
	mutate := c.callback(func(args []js.Value) {
		if len(args) > 0 && viewportMutationRelevant(args[0]) {
			c.requestScan()
		}
	})
	c.io = win.Get("IntersectionObserver").New(intersect, map[string]any{"rootMargin": "0px", "threshold": 0})
	c.mutations = win.Get("MutationObserver").New(mutate)
	c.on(doc, "visibilitychange", func() { c.sync() })
	c.on(win, "pagehide", func() { c.hidden = true; c.pause() })
	c.on(win, "pageshow", func() { c.hidden = false; c.sync() })
	for _, event := range []string{"online", "offline"} {
		c.on(win, event, func() { c.sync() })
	}
	for _, event := range []string{"scroll", "resize"} {
		c.on(win, event, func() { c.requestScan() })
	}
	connection := viewportConnection()
	if connection.Truthy() && connection.Get("addEventListener").Type() == js.TypeFunction {
		c.on(connection, "change", func() { c.sync() })
	}
	for i := 0; i < viewportWorkers; i++ {
		go c.worker(i)
	}
	c.sync()
}
func (c *viewportController) callback(fn func([]js.Value)) js.Func {
	f := js.FuncOf(func(_ js.Value, args []js.Value) any {
		defer func() {
			if v := recover(); v != nil {
				win.Get("console").Call("error", fmt.Sprint("Next.go viewport prefetch: ", v))
			}
		}()
		if !c.disposed {
			fn(args)
		}
		return nil
	})
	c.callbacks = append(c.callbacks, f)
	return f
}
func (c *viewportController) on(target js.Value, event string, fn func()) {
	f := c.callback(func([]js.Value) { fn() })
	target.Call("addEventListener", event, f)
	c.listeners = append(c.listeners, viewportListener{target, event, f})
}

// Text-only updates cannot create links or change eligibility. Inspect a bounded
// record/node prefix; overflow conservatively schedules a coalesced rescan.
func viewportMutationRelevant(records js.Value) bool {
	budget := viewportScanSlice
	for i := 0; i < records.Length(); i++ {
		if budget == 0 {
			return true
		}
		budget--
		record := records.Index(i)
		if record.Get("type").String() != "childList" {
			return true
		}
		for _, kind := range []string{"addedNodes", "removedNodes"} {
			nodes := record.Get(kind)
			for j := 0; j < nodes.Length(); j++ {
				if budget == 0 {
					return true
				}
				budget--
				typ := nodes.Index(j).Get("nodeType").Int()
				if typ != 3 && typ != 8 {
					return true
				}
			}
		}
	}
	return false
}
func viewportConnection() js.Value {
	navigator := win.Get("navigator")
	if !navigator.Truthy() {
		return js.Undefined()
	}
	for _, name := range []string{"connection", "mozConnection", "webkitConnection"} {
		if v := navigator.Get(name); v.Truthy() {
			return v
		}
	}
	return js.Undefined()
}
func viewportAllowed() bool {
	if moduleMode || draftFrozen || routeManifest.Draft || routeManifest.Development || doc.Get("visibilityState").String() != "visible" {
		return false
	}
	navigator := win.Get("navigator")
	if navigator.Truthy() {
		if online := navigator.Get("onLine"); online.Type() == js.TypeBoolean && !online.Bool() {
			return false
		}
	}
	if connection := viewportConnection(); connection.Truthy() {
		if saved := connection.Get("saveData"); saved.Type() == js.TypeBoolean && saved.Bool() {
			return false
		}
		if speed := connection.Get("effectiveType").String(); speed == "2g" || speed == "slow-2g" {
			return false
		}
	}
	return true
}
func (c *viewportController) sync() {
	if c.disposed || c.hidden || !viewportAllowed() {
		c.pause()
		return
	}
	if !c.active {
		c.active = true
		c.generation++
		c.mutations.Call("observe", doc.Get("body"), map[string]any{"childList": true, "subtree": true, "attributes": true, "attributeFilter": []any{"href", "target", "download", "data-nextgo-reload", "data-nextgo-prefetch", "class", "style", "hidden", "open"}})
	}
	c.requestScan()
}
func (c *viewportController) clearObserved() {
	c.io.Call("takeRecords")
	c.io.Call("disconnect")
	c.observed = nil
}
func (c *viewportController) pause() {
	if !c.active {
		return
	}
	c.active = false
	c.generation++
	c.clearObserved()
	c.mutations.Call("takeRecords")
	c.mutations.Call("disconnect")
	c.queue = nil
	c.dirty = false
	for _, flight := range c.flights {
		if flight.pending != nil && flight.pending.viewport && prefetches[flight.key] == flight.pending {
			flight.pending.cancel()
			delete(prefetches, flight.key)
			c.unmark(flight.candidate)
		}
	}
}
func (c *viewportController) candidate(node js.Value) (viewportCandidate, bool) {
	if !node.Truthy() || !node.Get("isConnected").Bool() {
		return viewportCandidate{}, false
	}
	root := doc.Call("getElementById", "nextgo-app")
	if !root.Truthy() || !root.Call("contains", node).Bool() {
		return viewportCandidate{}, false
	}
	if node.Call("getAttribute", "data-nextgo-prefetch").String() != "viewport" {
		return viewportCandidate{}, false
	}
	a, u, ok := eligibleLink(node)
	if !ok || !a.Equal(node) {
		return viewportCandidate{}, false
	}
	target := u.Get("href").String()
	if len(target) > viewportURLLimit {
		return viewportCandidate{}, false
	}
	old := win.Get("URL").New(currentURL)
	if old.Get("pathname").String() == u.Get("pathname").String() && old.Get("search").String() == u.Get("search").String() {
		return viewportCandidate{}, false
	}
	return viewportCandidate{node, requestURL(target), cacheEpoch}, true
}
func viewportAttempt(candidate viewportCandidate) string {
	return fmt.Sprint(candidate.epoch, "\x00", candidate.url)
}
func (c *viewportController) attempted(candidate viewportCandidate) bool {
	return c.attempts.Call("get", candidate.node).String() == viewportAttempt(candidate) || (c.recent != nil && c.recent[candidate.url] == candidate.epoch+1)
}
func (c *viewportController) mark(candidate viewportCandidate) {
	c.attempts.Call("set", candidate.node, viewportAttempt(candidate))
	if c.recent == nil {
		c.recent = map[string]uint64{}
	}
	if _, exists := c.recent[candidate.url]; !exists {
		if len(c.recentOrder) >= viewportObservedLimit {
			delete(c.recent, c.recentOrder[0])
			copy(c.recentOrder, c.recentOrder[1:])
			c.recentOrder = c.recentOrder[:len(c.recentOrder)-1]
		}
		c.recentOrder = append(c.recentOrder, candidate.url)
	}
	c.recent[candidate.url] = candidate.epoch + 1
}
func (c *viewportController) unmark(candidate viewportCandidate) {
	if candidate.node.Truthy() && c.attempts.Call("get", candidate.node).String() == viewportAttempt(candidate) {
		c.attempts.Call("delete", candidate.node)
	}
	if c.recent[candidate.url] == candidate.epoch+1 {
		delete(c.recent, candidate.url)
		for i, key := range c.recentOrder {
			if key == candidate.url {
				copy(c.recentOrder[i:], c.recentOrder[i+1:])
				c.recentOrder[len(c.recentOrder)-1] = ""
				c.recentOrder = c.recentOrder[:len(c.recentOrder)-1]
				break
			}
		}
	}
}
func (c *viewportController) enqueue(node js.Value) {
	if !c.active || !viewportAllowed() || len(c.queue) >= viewportQueueLimit {
		return
	}
	candidate, ok := c.candidate(node)
	if !ok {
		return
	}
	if c.attempted(candidate) {
		return
	}
	for _, queued := range c.queue {
		if queued.url == candidate.url && queued.epoch == candidate.epoch {
			return
		}
	}
	c.queue = append(c.queue, candidate)
	// Retiring queued targets frees observer capacity. Attempts are recorded at
	// dispatch, so dropped/offscreen jobs can be reconsidered without a fetch.
	c.io.Call("unobserve", node)
	for i, observed := range c.observed {
		if observed.node.Equal(node) {
			copy(c.observed[i:], c.observed[i+1:])
			c.observed[len(c.observed)-1] = viewportCandidate{}
			c.observed = c.observed[:len(c.observed)-1]
			break
		}
	}
	c.signal()
}
func (c *viewportController) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}
func (c *viewportController) requestScan() {
	if !c.active || c.disposed {
		return
	}
	c.dirty = true
	if c.scanning {
		return
	}
	c.scanning = true
	generation := c.generation
	go c.scan(generation)
}
func (c *viewportController) scan(generation uint64) {
	defer func() {
		c.scanning = false
		if c.active && c.dirty {
			c.requestScan()
		}
	}()
	for c.active && generation == c.generation && c.dirty {
		c.dirty = false
		retained := c.queue[:0]
		for _, queued := range c.queue {
			if current, ok := c.candidate(queued.node); ok && current.url == queued.url && current.epoch == queued.epoch {
				retained = append(retained, queued)
			}
		}
		for i := len(retained); i < len(c.queue); i++ {
			c.queue[i] = viewportCandidate{}
		}
		c.queue = retained
		c.clearObserved()
		root := doc.Call("getElementById", "nextgo-app")
		if !root.Truthy() {
			return
		}
		if root.Get("querySelector").Type() == js.TypeFunction && root.Call("querySelector", `a[data-nextgo-prefetch="viewport"][href]`).IsNull() {
			return
		}
		walker := doc.Call("createTreeWalker", root, 1)
		for c.active && generation == c.generation {
			// Every slice yields to the browser. No mutation records or NodeList of all
			// links are retained; the walker holds only its current position.
			time.Sleep(time.Millisecond)
			if !c.active || generation != c.generation {
				return
			}
			if !root.Get("isConnected").Bool() || !doc.Call("getElementById", "nextgo-app").Equal(root) {
				c.dirty = true
				break
			}
			finished := false
			for i := 0; i < viewportScanSlice; i++ {
				node := walker.Call("nextNode")
				if !node.Truthy() {
					finished = true
					break
				}
				if node.Get("tagName").String() != "A" {
					continue
				}
				candidate, ok := c.candidate(node)
				if !ok || c.attempted(candidate) {
					continue
				}
				if len(c.observed) >= viewportObservedLimit {
					time.Sleep(100 * time.Millisecond)
					if !c.active || generation != c.generation {
						return
					}
					c.clearObserved()
				}
				c.observed = append(c.observed, candidate)
				c.io.Call("observe", node)
			}
			if finished {
				break
			}
		}
	}
}
func viewportOnscreen(node js.Value) bool {
	rect := node.Call("getBoundingClientRect")
	return rect.Get("bottom").Float() > 0 && rect.Get("right").Float() > 0 && rect.Get("top").Float() < win.Get("innerHeight").Float() && rect.Get("left").Float() < win.Get("innerWidth").Float()
}
func (c *viewportController) worker(index int) {
	for {
		if _, open := <-c.wake; !open {
			return
		}
		if c.disposed {
			return
		}
		for len(c.queue) > 0 && c.active {
			// Always leave the synchronous observer callback before network setup.
			time.Sleep(time.Millisecond)
			if !c.active || !viewportAllowed() {
				c.sync()
				break
			}
			if len(c.queue) == 0 {
				break
			}
			candidate := c.queue[0]
			c.queue[0] = viewportCandidate{}
			c.queue = c.queue[1:]
			latest, ok := c.candidate(candidate.node)
			if !ok || latest.url != candidate.url || candidate.epoch != cacheEpoch || c.attempted(candidate) || !viewportOnscreen(candidate.node) {
				c.requestScan()
				continue
			}
			pending := prefetchWithOrigin(candidate.url, true)
			if pending == nil {
				if _, cached := pageCache.get(candidate.url); cached {
					c.mark(candidate)
				} else {
					time.Sleep(100 * time.Millisecond)
				}
				c.requestScan()
				continue
			}
			c.mark(candidate)
			c.flights[index] = viewportFlight{candidate.url, pending, candidate}
			c.requestScan()
			candidate = viewportCandidate{}
			c.signal()
			<-pending.done // Cancellation cannot release a physical worker early.
			c.flights[index] = viewportFlight{}
		}
	}
}
