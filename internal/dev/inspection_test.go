package dev

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/internal/project"
)

func inspectionRequest(s *supervisor, mutate func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "http://localhost:3000"+InspectionPath, nil)
	r.RemoteAddr = "127.0.0.1:40000"
	if mutate != nil {
		mutate(r)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestInspectionLocalAccessPolicy(t *testing.T) {
	s := &supervisor{authority: "127.0.0.1:3000"}
	tests := []struct {
		name   string
		mutate func(*http.Request)
		status int
	}{
		{"CLI", nil, 200},
		{"same origin", func(r *http.Request) {
			r.Header.Set("Origin", "http://localhost:3000")
			r.Header.Set("Sec-Fetch-Site", "same-origin")
		}, 200},
		{"ipv6 peer", func(r *http.Request) { r.RemoteAddr = "[::1]:40000" }, 200},
		{"remote peer", func(r *http.Request) { r.RemoteAddr = "192.0.2.1:40000" }, 403},
		{"forwarded loopback", func(r *http.Request) { r.RemoteAddr = "192.0.2.1:40000"; r.Header.Set("X-Forwarded-For", "127.0.0.1") }, 403},
		{"peer missing port", func(r *http.Request) { r.RemoteAddr = "127.0.0.1" }, 403},
		{"peer DNS", func(r *http.Request) { r.RemoteAddr = "localhost:40000" }, 403},
		{"peer unspecified", func(r *http.Request) { r.RemoteAddr = "0.0.0.0:40000" }, 403},
		{"host rebinding", func(r *http.Request) { r.Host = "attacker.example:3000" }, 403},
		{"host wrong port", func(r *http.Request) { r.Host = "localhost:3001" }, 403},
		{"cross origin", func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") }, 403},
		{"other local origin", func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:3000") }, 403},
		{"opaque origin", func(r *http.Request) { r.Header.Set("Origin", "null") }, 403},
		{"origin with path", func(r *http.Request) { r.Header.Set("Origin", "http://localhost:3000/") }, 403},
		{"duplicate origin", func(r *http.Request) {
			r.Header.Add("Origin", "http://localhost:3000")
			r.Header.Add("Origin", "http://localhost:3000")
		}, 403},
		{"cross site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
		{"same site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") }, 403},
		{"POST", func(r *http.Request) {
			r.Method = http.MethodPost
			r.Body = io.NopCloser(strings.NewReader("private request body"))
		}, 405},
		{"OPTIONS", func(r *http.Request) { r.Method = http.MethodOptions }, 405},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w := inspectionRequest(s, test.mutate)
			if w.Code != test.status {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("inspection enabled CORS")
			}
			if strings.Contains(w.Body.String(), "private request body") {
				t.Fatal("request body leaked")
			}
		})
	}
	explicit := &supervisor{authority: "192.168.1.2:3000"}
	w := inspectionRequest(explicit, func(r *http.Request) { r.Host = "192.168.1.2:3000" })
	if w.Code != 403 {
		t.Fatalf("explicit remote bind exposed inspection: %d", w.Code)
	}
	for _, host := range []string{"127.0.0.2:3000", "[::1]:3000", "LOCALHOST:3000"} {
		w := inspectionRequest(s, func(r *http.Request) { r.Host = host })
		if w.Code != 200 {
			t.Fatalf("loopback host %s: %d", host, w.Code)
		}
	}
}

func TestInspectionLastGoodBuildAndRecovery(t *testing.T) {
	secret := "PRIVATE_SOURCE_ENV_REQUEST_PROP_COMPILER_SECRET"
	p := &project.Project{Root: "/private/" + secret, Import: secret, ModulePath: secret,
		Routes: []project.Route{{Pattern: "/posts/:slug", Import: secret, Layouts: []string{secret},
			Slots: []project.Slot{{ID: secret, Name: "sidebar", Base: "/posts", Routes: []project.Route{{Pattern: "/posts/:slug", Import: secret}}, Intercepts: []project.Route{{Pattern: "/preview", Import: secret}}}}}},
		Components: []project.Component{{Name: "counter", Import: secret, Lazy: true}}}
	s := &supervisor{authority: "127.0.0.1:3000", output: io.Discard, id: "last-good", generation: "generation-one", proxy: &httputil.ReverseProxy{}}
	s.inspection.graph = publicInspectionGraph(p)
	s.inspectionTransitionLocked("ready", "")
	p.Routes[0].Pattern = "/changed-after-publication"
	read := func() Inspection {
		t.Helper()
		w := inspectionRequest(s, func(r *http.Request) { r.Header.Set("Cookie", "session="+secret); r.URL.RawQuery = "props=" + secret })
		if w.Code != 200 || strings.Contains(w.Body.String(), secret) {
			t.Fatalf("unsafe inspection: %d %s", w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("missing metadata response guards")
		}
		var snapshot Inspection
		if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	ready := read()
	if ready.State != "ready" || ready.Stale || !ready.Serving || ready.Graph.Nodes[0].Pattern != "/posts/:slug" {
		t.Fatalf("ready snapshot: %+v", ready)
	}
	s.inspectionTransitionLocked("building", "")
	building := read()
	if building.State != "building" || !building.Stale || !building.Serving || building.BuildID != ready.BuildID {
		t.Fatalf("rebuild lost last good: %+v", building)
	}
	s.buildError = secret
	s.inspectionTransitionLocked("failed", "build_failed")
	failed := read()
	if failed.Problem != "build_failed" || !failed.Serving || !failed.Stale || failed.Revision <= building.Revision {
		t.Fatalf("failed snapshot: %+v", failed)
	}
	s.id, s.generation = "recovered", "generation-two"
	s.buildError = ""
	s.inspection.graph = publicInspectionGraph(&project.Project{Routes: []project.Route{{Pattern: "/recovered", API: true}}})
	s.inspectionTransitionLocked("ready", "")
	recovered := read()
	if recovered.State != "ready" || recovered.Stale || recovered.Problem != "" || recovered.BuildID != "recovered" || recovered.Graph.Nodes[0].Kind != "api" {
		t.Fatalf("recovery: %+v", recovered)
	}
	c := &child{done: make(chan struct{})}
	close(c.done)
	s.exited(c)
	exited := read()
	if exited.State != "failed" || exited.Problem != "server_exited" || exited.Serving || !exited.Stale {
		t.Fatalf("exit: %+v", exited)
	}
}

func TestInspectionGraphBoundsAndReferences(t *testing.T) {
	p := &project.Project{}
	for i := 0; i < maxInspectionNodes*2; i++ {
		p.Routes = append(p.Routes, project.Route{Pattern: "/" + strings.Repeat("<", maxInspectionString-1), Layouts: []string{strings.Repeat("private", i%20+1)}})
	}
	p.Components = []project.Component{{Name: strings.Repeat("x", maxInspectionString+1)}, {Name: "control\x00private"}}
	graph := publicInspectionGraph(p)
	data, err := json.Marshal(Inspection{Version: 1, State: "ready", Graph: graph})
	if err != nil || len(data) > maxInspectionBytes || !graph.Truncated || len(graph.Nodes) > maxInspectionNodes || len(graph.Edges) > maxInspectionEdges {
		t.Fatalf("unbounded graph: bytes=%d nodes=%d edges=%d truncated=%v err=%v", len(data), len(graph.Nodes), len(graph.Edges), graph.Truncated, err)
	}
	ids := map[string]bool{}
	for _, node := range graph.Nodes {
		ids[node.ID] = true
		if len(node.Name) > maxInspectionString || len(node.Pattern) > maxInspectionString {
			t.Fatal("unbounded string")
		}
	}
	for _, edge := range graph.Edges {
		if !ids[edge.From] || !ids[edge.To] {
			t.Fatalf("dangling edge: %+v", edge)
		}
	}
	invalid := publicInspectionGraph(&project.Project{Components: []project.Component{{Name: "bad\x00name"}, {Name: strings.Repeat("x", maxInspectionString+1)}}})
	if !invalid.Truncated || len(invalid.Nodes) != 0 {
		t.Fatalf("unsafe strings accepted: %+v", invalid)
	}
}

func TestInspectionGraphSeparatesTemplatesAndSharedLayouts(t *testing.T) {
	graph := publicInspectionGraph(&project.Project{Routes: []project.Route{
		{Pattern: "/a", Layouts: []string{"private/module/app", "private/module/app"}, Templates: map[int]string{1: "/"}},
		{Pattern: "/b", Layouts: []string{"private/module/app", "private/module/app"}, Templates: map[int]string{1: "/"}},
	}})
	if len(graph.Nodes) != 4 || len(graph.Edges) != 4 || graph.Nodes[1].Kind != "layout" || graph.Nodes[2].Kind != "template" {
		t.Fatalf("template/layout identity collapsed: %+v", graph)
	}
	if graph.Edges[0].To != graph.Edges[2].To || graph.Edges[1].To != graph.Edges[3].To || graph.Edges[0].To == graph.Edges[1].To {
		t.Fatalf("shared wrappers or ordering lost: %+v", graph.Edges)
	}
}

func TestInspectionLoopbackHTTPAndHead(t *testing.T) {
	// Bind explicitly before serving so the real request Host policy uses the
	// actual listener authority. A real TCP request also supplies RemoteAddr.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("loopback listener unavailable: %v", err)
	}
	s := &supervisor{authority: listener.Addr().String()}
	server := httptest.NewUnstartedServer(s)
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()
	snapshot, err := Inspect(context.Background(), s.authority)
	if err != nil || snapshot.Version != 1 || snapshot.State != "building" {
		t.Fatalf("real local inspection: %+v %v", snapshot, err)
	}
	url := "http://" + s.authority + InspectionPath
	r, _ := http.NewRequest(http.MethodHead, url, nil)
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 || len(body) != 0 || response.Header.Get("Content-Length") == "" {
		t.Fatalf("HEAD: %d %q", response.StatusCode, body)
	}
}

func TestInspectionClientRejectsNonlocalAndUnboundedResponses(t *testing.T) {
	for _, authority := range []string{"example.com:3000", "192.0.2.1:3000", "0.0.0.0:3000", "localhost", "localhost:03000", "localhost:0", "user@localhost:3000", "localhost:3000/path"} {
		if _, err := Inspect(context.Background(), authority); err == nil {
			t.Fatalf("accepted address %q", authority)
		}
	}
	for _, variant := range []string{"redirect", "oversized", "invalid", "version", "error"} {
		t.Run(variant, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch variant {
				case "redirect":
					http.Redirect(w, r, "http://192.0.2.1/secret", http.StatusFound)
				case "oversized":
					io.WriteString(w, strings.Repeat("x", maxInspectionBytes+1))
				case "invalid":
					io.WriteString(w, "compiler private error body")
				case "version":
					io.WriteString(w, `{"version":99}`)
				case "error":
					http.Error(w, "compiler private error body", 500)
				}
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL)
			_, err := Inspect(context.Background(), u.Host)
			if err == nil || strings.Contains(err.Error(), "private error") {
				t.Fatalf("unsafe upstream result: %v", err)
			}
		})
	}
}

func TestProductionApplicationDoesNotExposeInspection(t *testing.T) {
	for _, development := range []bool{false, true} {
		app, err := nextgo.New(nextgo.Config{Dev: development})
		if err != nil {
			t.Fatal(err)
		}
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodHead} {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, httptest.NewRequest(method, "http://localhost:3000"+InspectionPath, nil))
			if w.Code != http.StatusNotFound || strings.Contains(w.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("application (development=%v) exposed inspection: %s %d %s", development, method, w.Code, w.Body.String())
			}
		}
	}
}
