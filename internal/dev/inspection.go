package dev

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/project"
)

// InspectionPath belongs to the development supervisor, never the application.
const InspectionPath = "/_nextgo/inspect"

const (
	inspectionVersion   = 1
	maxInspectionBytes  = 1 << 20
	maxInspectionNodes  = 2048
	maxInspectionEdges  = 4096
	maxInspectionString = 512
	maxInspectionDepth  = 16
	// Reserve room for the fixed status envelope, IDs, and JSON punctuation.
	maxInspectionGraphBytes = maxInspectionBytes - 4096
)

// Inspection is the bounded, public metadata snapshot served by nextgo dev.
// Graph describes the last successful build, including while a rebuild fails.
type Inspection struct {
	Version         int             `json:"version"`
	Revision        uint64          `json:"revision"`
	State           string          `json:"state"`
	Problem         string          `json:"problem,omitempty"`
	BuildID         string          `json:"buildId,omitempty"`
	Generation      string          `json:"generation,omitempty"`
	RefreshRevision uint64          `json:"refreshRevision"`
	RefreshEnabled  bool            `json:"refreshEnabled"`
	Serving         bool            `json:"serving"`
	Stale           bool            `json:"stale"`
	Graph           InspectionGraph `json:"graph"`
}

type InspectionGraph struct {
	Nodes     []InspectionNode `json:"nodes"`
	Edges     []InspectionEdge `json:"edges"`
	Truncated bool             `json:"truncated"`
}

type InspectionNode struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Pattern   string `json:"pattern,omitempty"`
	Name      string `json:"name,omitempty"`
	Lazy      bool   `json:"lazy,omitempty"`
	Default   bool   `json:"default,omitempty"`
	Prerender bool   `json:"prerender,omitempty"`
	Regions   bool   `json:"regions,omitempty"`
}

type InspectionEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

type inspectionState struct {
	state, problem string
	revision       uint64
	graph          InspectionGraph
}

// Caller holds s.mu. Existing development diagnostics retain error details;
// only these fixed diagnostic codes are part of the inspection protocol.
func (s *supervisor) inspectionTransitionLocked(state, problem string) {
	s.inspection.state, s.inspection.problem = state, problem
	s.inspection.revision++
}

func (s *supervisor) inspectionSnapshot() Inspection {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.inspection.state
	if state == "" {
		state = "building"
	}
	graph := s.inspection.graph
	if graph.Nodes == nil {
		graph.Nodes = []InspectionNode{}
	}
	if graph.Edges == nil {
		graph.Edges = []InspectionEdge{}
	}
	snapshot := Inspection{Version: inspectionVersion, Revision: s.inspection.revision,
		State: state, Problem: s.inspection.problem, BuildID: s.id, Generation: s.generation,
		Serving: s.proxy != nil, Stale: s.id != "" && state != "ready", Graph: graph}
	if s.refresh != nil {
		snapshot.RefreshEnabled = true
		snapshot.RefreshRevision = s.refresh.Revision
	}
	return snapshot
}

func (s *supervisor) inspect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !s.allowedInspectionRequest(r) {
		http.Error(w, "development inspection requires a local same-origin request", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := json.Marshal(s.inspectionSnapshot())
	if err != nil || len(data) > maxInspectionBytes {
		http.Error(w, "inspection unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method == http.MethodGet {
		_, _ = w.Write(data)
	}
}

func (s *supervisor) allowedInspectionRequest(r *http.Request) bool {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(peer)
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	if !s.allowedHost(r.Host) {
		return false
	}
	s.mu.Lock()
	zoned := s.zoneHosts != nil
	s.mu.Unlock()
	if !zoned {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			return false
		}
		ip = net.ParseIP(host)
		if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			return false
		}
	}
	origins := r.Header.Values("Origin")
	if len(origins) > 1 || len(origins) == 1 && origins[0] != "http://"+r.Host {
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "none", "same-origin":
		return true
	default:
		return false
	}
}

// Zone graphs project only bounded public declarations and route/component
// metadata. Native roots/imports, tenant values and configuration stay private.
func publicBuildInspectionGraph(build *project.Build) InspectionGraph {
	if build == nil || build.Zones == nil {
		if build == nil {
			return publicInspectionGraph(nil)
		}
		return publicInspectionGraph(build.Project)
	}
	b := inspectionGraphBuilder{graph: InspectionGraph{Nodes: []InspectionNode{}, Edges: []InspectionEdge{}}, budget: maxInspectionNodes * 8}
	projects := make(map[string]*project.Project, len(build.ZoneProjects))
	for _, p := range build.ZoneProjects {
		if p != nil {
			projects[p.Root] = p
		}
	}
	for _, zone := range build.Zones.Zones {
		if !b.step() {
			break
		}
		name := b.publicString(zone.ID)
		pattern := zone.BasePath
		if pattern == "" {
			pattern = "/"
		}
		pattern = b.publicString(pattern)
		if name == "" || pattern == "" {
			continue
		}
		id := b.node(InspectionNode{Kind: "zone", Name: name, Pattern: pattern})
		p := projects[zone.Root]
		if p == nil {
			b.graph.Truncated = true
			continue
		}
		b.wrappers, b.slots = map[string]string{}, map[string]string{}
		for _, route := range p.Routes {
			if b.budget <= 0 {
				b.graph.Truncated = true
				break
			}
			b.edge(id, b.route(route, 0), "route")
		}
		for _, component := range p.Components {
			if !b.step() {
				break
			}
			name := b.publicString(component.Name)
			if name != "" {
				b.edge(id, b.node(InspectionNode{Kind: "component", Name: name, Lazy: component.Lazy}), "component")
			}
		}
	}
	return b.graph
}

// Inspection never serializes project structures. Source paths, import names,
// configuration, action inputs, and compiler errors have no protocol fields.
func publicInspectionGraph(p *project.Project) InspectionGraph {
	b := inspectionGraphBuilder{graph: InspectionGraph{Nodes: []InspectionNode{}, Edges: []InspectionEdge{}},
		wrappers: map[string]string{}, slots: map[string]string{}, budget: maxInspectionNodes * 8}
	if p == nil {
		return b.graph
	}
	for _, route := range p.Routes {
		if b.budget <= 0 {
			b.graph.Truncated = true
			break
		}
		b.route(route, 0)
	}
	for _, component := range p.Components {
		if !b.step() {
			break
		}
		name := b.publicString(component.Name)
		if name == "" {
			continue
		}
		b.node(InspectionNode{Kind: "component", Name: name, Lazy: component.Lazy})
	}
	return b.graph
}

type inspectionGraphBuilder struct {
	graph           InspectionGraph
	wrappers, slots map[string]string
	budget          int
	bytes           int
}

func (b *inspectionGraphBuilder) step() bool {
	b.budget--
	if b.budget < 0 || len(b.graph.Nodes) >= maxInspectionNodes {
		b.graph.Truncated = true
		return false
	}
	return true
}

func (b *inspectionGraphBuilder) publicString(s string) string {
	if len(s) > maxInspectionString || !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		b.graph.Truncated = true
		return ""
	}
	return s
}

func (b *inspectionGraphBuilder) node(n InspectionNode) string {
	if len(b.graph.Nodes) >= maxInspectionNodes {
		b.graph.Truncated = true
		return ""
	}
	n.ID = fmt.Sprintf("n%d", len(b.graph.Nodes))
	encoded, _ := json.Marshal(n)
	if b.bytes+len(encoded)+1 > maxInspectionGraphBytes {
		b.graph.Truncated = true
		b.budget = 0
		return ""
	}
	b.bytes += len(encoded) + 1
	b.graph.Nodes = append(b.graph.Nodes, n)
	return n.ID
}

func (b *inspectionGraphBuilder) edge(from, to, kind string) {
	if from == "" || to == "" {
		return
	}
	if len(b.graph.Edges) >= maxInspectionEdges {
		b.graph.Truncated = true
		return
	}
	edge := InspectionEdge{From: from, To: to, Kind: kind}
	encoded, _ := json.Marshal(edge)
	if b.bytes+len(encoded)+1 > maxInspectionGraphBytes {
		b.graph.Truncated = true
		b.budget = 0
		return
	}
	b.bytes += len(encoded) + 1
	b.graph.Edges = append(b.graph.Edges, edge)
}

func (b *inspectionGraphBuilder) route(route project.Route, depth int) string {
	if !b.step() {
		return ""
	}
	if depth > maxInspectionDepth {
		b.graph.Truncated = true
		return ""
	}
	pattern := b.publicString(route.Pattern)
	if pattern == "" {
		return ""
	}
	kind := "page"
	if route.API {
		kind = "api"
	}
	id := b.node(InspectionNode{Kind: kind, Pattern: pattern, Default: route.Default, Prerender: route.Prerender, Regions: route.Regions})
	for index, wrapper := range route.Layouts {
		if !b.step() {
			break
		}
		kind := "layout"
		if _, template := route.Templates[index]; template {
			kind = "template"
		}
		key := kind + "\x00" + wrapper
		layout := b.wrappers[key]
		if layout == "" {
			layout = b.node(InspectionNode{Kind: kind})
			b.wrappers[key] = layout
		}
		b.edge(id, layout, kind)
	}
	for _, slot := range route.Slots {
		if !b.step() {
			break
		}
		slotID := b.slots[slot.ID]
		if slotID != "" {
			b.edge(id, slotID, "slot")
			continue
		}
		name, base := b.publicString(slot.Name), b.publicString(slot.Base)
		if name == "" || base == "" {
			continue
		}
		slotID = b.node(InspectionNode{Kind: "slot", Name: name, Pattern: base})
		b.slots[slot.ID] = slotID
		b.edge(id, slotID, "slot")
		for _, branch := range slot.Routes {
			if b.budget <= 0 {
				break
			}
			b.edge(slotID, b.route(branch, depth+1), "route")
		}
		for _, branch := range slot.Intercepts {
			if b.budget <= 0 {
				break
			}
			b.edge(slotID, b.route(branch, depth+1), "intercept")
		}
		if slot.DefaultRoute != nil {
			b.edge(slotID, b.route(*slot.DefaultRoute, depth+1), "default")
		}
	}
	return id
}
