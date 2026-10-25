//go:build !js

package nextgo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Suhaibinator/Next.go/internal/releaseassets"
	"github.com/Suhaibinator/Next.go/internal/zonecontract"
	"github.com/Suhaibinator/Next.go/ui"
)

type isolatedNamespaceCase struct {
	name, path, method, requestBody, wantBody string
	status                                    int
	dynamic                                   bool
}

func isolatedNamespaceCases(prefix string) []isolatedNamespaceCase {
	return []isolatedNamespaceCase{
		{name: "canonical-unversioned-public", path: "/team/assets/style.css", status: 404},
		{name: "canonical-unversioned-runtime", path: "/team/_nextgo/" + releaseBoundaryRuntime + "/app.wasm", status: 404},
		{name: "encoded-unversioned-public", path: "/team/%61ssets/style.css", status: 404},
		{name: "encoded-unversioned-runtime-namespace", path: "/team/%5fnextgo/" + releaseBoundaryRuntime + "/app.wasm", status: 404},
		{name: "encoded-unversioned-runtime-id", path: "/team/_nextgo/%30" + releaseBoundaryRuntime[1:] + "/app.wasm", status: 404},
		{name: "encoded-versioned-public-namespace", path: prefix + "/%61ssets/style.css", status: 404},
		{name: "encoded-mount-not-an-alias", path: "/%74eam/_nextgo/releases/release-one/assets/style.css", status: 404},
		{name: "versioned-space-filename", path: prefix + "/assets/space%20name.txt", status: 200, wantBody: "release-local-space-name"},
		{name: "versioned-unicode-filename", path: prefix + "/assets/%C3%A9.txt", status: 200, wantBody: "release-local-unicode-name"},
		{name: "unknown-runtime", path: "/team/_nextgo/unknown/app.wasm", status: 404},
		{name: "uppercase-runtime", path: "/team/_nextgo/0123456789ABCDEF0123/app.wasm", status: 404},
		{name: "encoded-unknown-runtime", path: "/team/%5fnextgo/unknown/app.wasm", status: 404},
		{name: "encoded-reserved-root-api", path: "/team/%5fnextgo/actions/save", status: 404},
		{name: "encoded-service-name-denied", path: "/team/_nextgo/%61ctions/save", status: 404},
		{name: "reserved-root", path: "/team/_nextgo", status: 404},
		{name: "normal-api-body-control", path: "/team/api/ping", method: "POST", requestBody: "request-body", status: 200, wantBody: "dynamic:request-body", dynamic: true},
		{name: "normal-encoded-page-control", path: "/team/page/%61bout", status: 200, wantBody: "page:about", dynamic: true},
	}
}
func isolatedNamespaceConfig(t *testing.T) (ZoneReleaseConfig, *atomic.Int32) {
	t.Helper()
	declaration, assets := releaseBoundaryConfig(t)
	assets["public/space name.txt"] = &fstest.MapFile{Data: []byte("release-local-space-name")}
	assets["public/é.txt"] = &fstest.MapFile{Data: []byte("release-local-unicode-name")}
	_, digest, err := releaseassets.Freeze(context.Background(), assets, releaseBoundaryRuntime)
	if err != nil {
		t.Fatal(err)
	}
	declaration.Release.AssetsSHA256 = digest
	calls := new(atomic.Int32)
	declaration.Zone.Config.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("X-Isolated-Middleware", "entered")
			http.SetCookie(w, &http.Cookie{Name: "namespace_fixture", Value: "entered", Path: "/team"})
			next.ServeHTTP(w, r)
		})
	}}
	declaration.Zone.Config.Routes = []Route{
		{Pattern: "/api/ping", API: func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "body read failed", 400)
				return
			}
			_, _ = io.WriteString(w, "dynamic:"+string(body))
		}},
		{Pattern: "/page/:slug", Page: func(c *Context) (Page, error) { return Page{Body: ui.Text("page:" + c.Params["slug"])}, nil }},
	}
	return declaration, calls
}
func isolatedNamespaceClient(t *testing.T) *http.Client {
	t.Helper()
	transport := &http.Transport{DisableCompression: true, Proxy: nil}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 3 * time.Second}
}
func isolatedNamespaceObserve(t *testing.T, layer string, client *http.Client, base string, declaration ZoneReleaseConfig, check isolatedNamespaceCase, calls *atomic.Int32, backendCalls *atomic.Int32) {
	t.Helper()
	before := calls.Load()
	var backendBefore int32
	if backendCalls != nil {
		backendBefore = backendCalls.Load()
	}
	method := check.method
	if method == "" {
		method = "GET"
	}
	request, err := http.NewRequest(method, base+check.path, strings.NewReader(check.requestBody))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept-Encoding", "identity")
	if layer == "endpoint" {
		zonecontract.SetExpectation(request.Header, declaration.Release.Expectation())
	} else {
		request.Host = declaration.Release.Zone.Host
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, readError := io.ReadAll(response.Body)
	response.Body.Close()
	digest := sha256.Sum256(data)
	var wantCalls int32
	if check.dynamic {
		wantCalls = 1
	}
	var backendDelta, wantBackend int32
	if backendCalls != nil {
		backendDelta = backendCalls.Load() - backendBefore
		if check.status == 200 {
			wantBackend = 1
		}
	}
	record := struct {
		Layer                   string      `json:"layer"`
		Path                    string      `json:"path"`
		EscapedPath             string      `json:"escapedPath"`
		Status                  int         `json:"status"`
		ExpectedStatus          int         `json:"expectedStatus"`
		Headers                 http.Header `json:"headers"`
		BodySHA256              string      `json:"bodySHA256"`
		MiddlewareCalls         int32       `json:"middlewareCalls"`
		ExpectedMiddlewareCalls int32       `json:"expectedMiddlewareCalls"`
		BackendCalls            int32       `json:"backendCalls"`
		ExpectedBackendCalls    int32       `json:"expectedBackendCalls"`
	}{layer, check.path, request.URL.EscapedPath(), response.StatusCode, check.status, response.Header.Clone(), hex.EncodeToString(digest[:]), calls.Load() - before, wantCalls, backendDelta, wantBackend}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if directory := os.Getenv("NEXTGO_1XX_EVIDENCE_DIR"); directory != "" {
		if err := os.WriteFile(filepath.Join(directory, layer+"-"+check.name+".json"), append(encoded, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if response.StatusCode != check.status {
		t.Errorf("encoded namespace response status=%d want%d path=%s", response.StatusCode, check.status, check.path)
	}
	if record.MiddlewareCalls != wantCalls {
		t.Errorf("reserved alias entered dynamic application: calls=%d want%d", record.MiddlewareCalls, wantCalls)
	}
	if !check.dynamic && (response.Header.Get("X-Isolated-Middleware") != "" || len(response.Header.Values("Set-Cookie")) != 0) {
		t.Error("terminal reserved rejection or asset response carried dynamic cookie/marker")
	}
	if backendCalls != nil && backendDelta != wantBackend {
		t.Errorf("ingress encoded namespace dispatched: backendCalls=%d want%d", backendDelta, wantBackend)
	}
	if readError != nil {
		t.Error(readError)
	}
	if check.wantBody != "" {
		if check.name == "normal-encoded-page-control" {
			if !strings.Contains(string(data), check.wantBody) {
				t.Error("encoded page did not preserve ordinary routing")
			}
		} else if string(data) != check.wantBody {
			t.Error("positive bytes/body control changed", string(data))
		}
	}
	if layer == "endpoint" && zonecontract.CheckExpectation(response.Header, declaration.Release.Expectation()) != nil {
		t.Error("private release identity lost")
	}
}
func TestIsolatedZoneReleaseBoundaryEncodedUnversionedNamespaces(t *testing.T) {
	declaration, calls := isolatedNamespaceConfig(t)
	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)
	declaration.BackendHosts = []string{server.Listener.Addr().String()}
	server.Config.Handler = releaseBoundaryNew(t, declaration)
	server.Start()
	client := isolatedNamespaceClient(t)
	for _, check := range isolatedNamespaceCases(declaration.Release.AssetPrefix) {
		t.Run(check.name, func(t *testing.T) {
			isolatedNamespaceObserve(t, "endpoint", client, server.URL, declaration, check, calls, nil)
		})
	}
}
func TestIsolatedZoneIngressRejectsEncodedUnversionedNamespacesBeforeDispatch(t *testing.T) {
	declaration, calls := isolatedNamespaceConfig(t)
	var backendCalls atomic.Int32
	backend := httptest.NewUnstartedServer(nil)
	t.Cleanup(backend.Close)
	declaration.BackendHosts = []string{backend.Listener.Addr().String()}
	release := releaseBoundaryNew(t, declaration)
	backend.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { backendCalls.Add(1); release.ServeHTTP(w, r) })
	backend.Start()
	manifest := ZoneIngressManifest{Version: zonecontract.Version, TopologySHA256: declaration.Release.TopologySHA256, Zones: []ZoneIngressZone{{Binding: declaration.Release.Zone, ActiveRelease: declaration.Release.ReleaseID, Releases: []ZoneIngressRelease{{Artifact: ZoneReleaseManifest{Release: declaration.Release, BinarySHA256: strings.Repeat("b", 64)}, Origin: backend.URL}}}}}
	ingress, err := NewZoneIngress(context.Background(), manifest, ZoneIngressOptions{TrustedTLSOffload: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ingress.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	front := httptest.NewServer(ingress)
	t.Cleanup(front.Close)
	client := isolatedNamespaceClient(t)
	for _, check := range isolatedNamespaceCases(declaration.Release.AssetPrefix) {
		t.Run(check.name, func(t *testing.T) {
			isolatedNamespaceObserve(t, "ingress", client, front.URL, declaration, check, calls, &backendCalls)
		})
	}
}
