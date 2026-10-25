package nextgo

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/actionwire"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/zonecontract"
)

func ingressArtifact(t *testing.T, id, prefix, release, origin string, documents ...string) zonecontract.IngressRelease {
	t.Helper()
	assetPrefix, err := zonecontract.ReleasePrefix(prefix, release)
	if err != nil {
		t.Fatal(err)
	}
	return zonecontract.IngressRelease{Origin: origin, Artifact: zonecontract.ReleaseManifest{BinarySHA256: strings.Repeat("b", 64), Release: zonecontract.ReleaseSpec{
		Version: 1, Zone: zonecontract.Binding{ID: id, Host: "site.test", BasePath: prefix}, ReleaseID: release, RuntimeID: strings.Repeat("a", 20), PublicOrigin: "http://site.test", AssetPrefix: assetPrefix,
		AssetsSHA256: strings.Repeat("a", 64), TopologySHA256: strings.Repeat("c", 64), DocumentPaths: documents, NavigationProtocol: navigation.ProtocolVersion, ActionProtocol: actionwire.Protocol,
	}}}
}
func ingressManifest(releases ...zonecontract.IngressRelease) zonecontract.IngressManifest {
	zones := map[string]int{}
	manifest := zonecontract.IngressManifest{Version: 1, TopologySHA256: strings.Repeat("c", 64)}
	for _, release := range releases {
		binding := release.Artifact.Release.Zone
		index, ok := zones[binding.ID]
		if !ok {
			index = len(manifest.Zones)
			zones[binding.ID] = index
			manifest.Zones = append(manifest.Zones, zonecontract.IngressZone{Binding: binding, ActiveRelease: release.Artifact.Release.ReleaseID})
		}
		manifest.Zones[index].Releases = append(manifest.Zones[index].Releases, release)
	}
	return manifest
}
func ingressTest(t *testing.T, manifest zonecontract.IngressManifest, options ZoneIngressOptions) *ZoneIngress {
	t.Helper()
	ingress, err := NewZoneIngress(context.Background(), manifest, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := ingress.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return ingress
}
func ingressRequest(ingress *ZoneIngress, method, path string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	ingress.ServeHTTP(response, httptest.NewRequest(method, "http://site.test"+path, nil))
	return response
}
func ingressIdentity(w http.ResponseWriter, r *http.Request) {
	zonecontract.SetExpectation(w.Header(), zonecontract.Expectation{ZoneID: r.Header.Get(zonecontract.HeaderZoneID), ReleaseID: r.Header.Get(zonecontract.HeaderReleaseID), TopologySHA256: r.Header.Get(zonecontract.HeaderTopology)})
}

func TestZoneIngressImmutableRoutingRetainedAssetsAndNoParentFallback(t *testing.T) {
	var rootCalls, oldCalls, childCalls atomic.Int32
	backend := func(counter *atomic.Int32, text string, status int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counter.Add(1)
			ingressIdentity(w, r)
			w.WriteHeader(status)
			_, _ = io.WriteString(w, text)
		}))
	}
	root, old, child := backend(&rootCalls, "root", 200), backend(&oldCalls, "old asset", 200), backend(&childCalls, "child missing", 404)
	defer root.Close()
	defer old.Close()
	defer child.Close()
	manifest := ingressManifest(ingressArtifact(t, "root", "", "new", root.URL, "/blog"), ingressArtifact(t, "root", "", "old", old.URL, "/blog"), ingressArtifact(t, "blog", "/blog", "blogA", child.URL))
	ingress := ingressTest(t, manifest, ZoneIngressOptions{})
	manifest.Zones[0].Releases[0].Origin = "http://invalid.test"
	manifest.Zones[0].ActiveRelease = "old"
	for path, text := range map[string]string{"/": "root", "/blogger": "root", "/_nextgo/releases/old/assets/app.css": "old asset", "/_nextgo/releases/new/_nextgo/" + strings.Repeat("a", 20) + "/client.wasm": "root"} {
		got := ingressRequest(ingress, "GET", path)
		if got.Code != 200 || got.Body.String() != text {
			t.Fatal(path, got.Code, got.Body.String())
		}
	}
	got := ingressRequest(ingress, "GET", "/blog/missing")
	if got.Code != 404 || got.Body.String() != "child missing" || rootCalls.Load() != 3 {
		t.Fatal("child response fell through", got.Code, got.Body.String(), rootCalls.Load())
	}
	before := rootCalls.Load() + oldCalls.Load() + childCalls.Load()
	for _, path := range []string{"/_nextgo/releases/missing/assets/x", "/_nextgo/releases/old/api/secret", "/_nextgo/releases/old/_nextgo/actions/save", "/_nextgo/releases/old/_nextgo/" + strings.Repeat("b", 20) + "/client.wasm", "/_nextgo/releases/old/assets/", "/assets/x", "/_nextgo/" + strings.Repeat("a", 20) + "/client.wasm", "/_nextgo/unknown/private", "/blog/_nextgo/releases/old/assets/x"} {
		if got := ingressRequest(ingress, "GET", path); got.Code != 404 {
			t.Fatal("reserved/unknown release escaped", path, got.Code)
		}
	}
	if got := ingressRequest(ingress, "POST", "/_nextgo/releases/old/assets/app.css"); got.Code != 404 {
		t.Fatal("retained release accepted unsafe method", got.Code)
	}
	if after := rootCalls.Load() + oldCalls.Load() + childCalls.Load(); after != before {
		t.Fatal("terminal asset rejection contacted an endpoint", before, after)
	}
}

func TestZoneIngressHostSchemeAndRequestGuardsBeforeDispatch(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		ingressIdentity(w, r)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	release := ingressArtifact(t, "one", "/app", "r1", server.URL)
	release.Artifact.Release.PublicOrigin = "https://site.test"
	ingress := ingressTest(t, ingressManifest(release), ZoneIngressOptions{})
	for _, tc := range []struct {
		host, path string
		status     int
	}{{"other.test", "/app", 421}, {"site.test@evil.test", "/app", 400}, {"site.test", "/application", 404}, {"site.test", "/app/%2e%2e", 400}, {"site.test", "/app/a%2Fb", 400}, {"site.test", "/app", 400}} {
		request := httptest.NewRequest("GET", tc.path, nil)
		request.Host = tc.host
		request.Header.Set("Forwarded", "proto=https;host=site.test")
		request.Header.Set("X-Forwarded-Proto", "https")
		response := httptest.NewRecorder()
		ingress.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatal(tc, response.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("rejected requests reached an endpoint")
	}
	request := httptest.NewRequest("GET", "https://site.test/app", nil)
	request.TLS = &tls.ConnectionState{}
	response := httptest.NewRecorder()
	ingress.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal("actual TLS rejected", response.Code)
	}
	offload := ingressTest(t, ingressManifest(release), ZoneIngressOptions{TrustedTLSOffload: true})
	if got := ingressRequest(offload, "GET", "/app"); got.Code != 200 {
		t.Fatal("explicit trusted TLS offload rejected", got.Code)
	}
	for _, header := range []string{"upgrade", "origin", "X-Nextgo-Action-Protocol", zonecontract.HeaderReleaseID} {
		request := httptest.NewRequest("GET", "http://site.test/app", nil)
		request.Header.Set("Connection", header)
		response := httptest.NewRecorder()
		offload.ServeHTTP(response, request)
		want := 400
		if header == "upgrade" {
			want = 501
		}
		if response.Code != want {
			t.Fatal(header, response.Code)
		}
	}
}

func TestZoneIngressPreservesPublicProtocolsCredentialsAndStripsPrivateHopFields(t *testing.T) {
	var actualHost string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actualHost = r.Host
		expectation := zonecontract.Expectation{ZoneID: "one", ReleaseID: "r1", TopologySHA256: strings.Repeat("c", 64)}
		if zonecontract.CheckExpectation(r.Header, expectation) != nil || r.Header.Get("Cookie") != "session=abc" || r.Header.Get("Authorization") != "Bearer abc" || r.Header.Get("Origin") != "http://site.test" || r.Header.Get("X-Nextgo-Action-View") != "1" || r.Header.Get("X-Nextgo-Protocol") != "3" || r.Header.Get("X-Remove") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-Host") != "" || r.URL.RawQuery != "q=1&q=2" {
			t.Error("request protocol/credentials/identity lost", r.Header)
		}
		connection, buffer, _ := w.(http.Hijacker).Hijack()
		defer connection.Close()
		_, _ = fmt.Fprintf(buffer, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: X-Discard, close\r\nX-Discard: discard\r\nSet-Cookie: one=1; Path=/\r\nSet-Cookie: two=2; Path=/\r\nX-Nextgo-Action-Protocol: 1\r\nX-Nextgo-Configuration: configuration\r\nX-Nextgo-Draft: off\r\n%s: 1\r\n%s: one\r\n%s: r1\r\n%s: %s\r\n\r\nok", zonecontract.HeaderProtocol, zonecontract.HeaderZoneID, zonecontract.HeaderReleaseID, zonecontract.HeaderTopology, strings.Repeat("c", 64))
		_ = buffer.Flush()
	}))
	defer server.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	var proxies atomic.Int32
	transport.Proxy = func(*http.Request) (*url.URL, error) { proxies.Add(1); return nil, errors.New("must not proxy") }
	ingress := ingressTest(t, ingressManifest(ingressArtifact(t, "one", "", "r1", server.URL)), ZoneIngressOptions{Transport: transport})
	request := httptest.NewRequest("GET", "http://site.test/who?q=1&q=2", nil)
	request.Header = http.Header{"Cookie": {"session=abc"}, "Authorization": {"Bearer abc"}, "Origin": {"http://site.test"}, "X-Nextgo-Action-View": {"1"}, "X-Nextgo-Protocol": {"3"}, "Connection": {"X-Remove"}, "X-Remove": {"drop"}, "Forwarded": {"host=evil"}, "X-Forwarded-Host": {"evil"}, "x-nextgo-zone-release": {"forged"}, zonecontract.HeaderReleaseID: {"forged2"}, "X-Nextgo-Zone-Future": {"private"}}
	response := httptest.NewRecorder()
	ingress.ServeHTTP(response, request)
	if response.Code != 200 || response.Body.String() != "ok" || actualHost != strings.TrimPrefix(server.URL, "http://") || proxies.Load() != 0 || transport.Proxy == nil || transport.DisableKeepAlives {
		t.Fatal(response.Code, actualHost, proxies.Load(), response.Body.String())
	}
	if len(response.Header().Values("Set-Cookie")) != 2 || response.Header().Get("X-Nextgo-Action-Protocol") != "1" || response.Header().Get("X-Nextgo-Configuration") != "configuration" || response.Header().Get("X-Nextgo-Draft") != "off" || response.Header().Get("X-Discard") != "" {
		t.Fatal("public headers lost", response.Header())
	}
	for name := range response.Header() {
		if zoneIngressPrivateHeader(name) {
			t.Fatal("private header relayed", name)
		}
	}
}

func TestZoneIngressRejectsIdentityAndAssetCookiesBeforePublicCommit(t *testing.T) {
	for _, kind := range []string{"missing", "wrong", "duplicate", "future", "trailer", "nomination", "upgrade", "asset-cookie", "asset-cookie-trailer", "known-length"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ingressIdentity(w, r)
				if kind == "nomination" {
					connection, buffer, _ := w.(http.Hijacker).Hijack()
					defer connection.Close()
					_, _ = fmt.Fprintf(buffer, "HTTP/1.1 200 OK\r\nContent-Length: 20\r\nConnection: X-Nextgo-Action-Protocol, close\r\nX-Nextgo-Action-Protocol: 1\r\n%s: 1\r\n%s: one\r\n%s: r1\r\n%s: %s\r\n\r\nbackend private data", zonecontract.HeaderProtocol, zonecontract.HeaderZoneID, zonecontract.HeaderReleaseID, zonecontract.HeaderTopology, strings.Repeat("c", 64))
					_ = buffer.Flush()
					return
				}
				switch kind {
				case "missing":
					w.Header().Del(zonecontract.HeaderReleaseID)
				case "wrong":
					w.Header().Set(zonecontract.HeaderReleaseID, "other")
				case "duplicate":
					w.Header().Add(zonecontract.HeaderReleaseID, "r1")
				case "future":
					w.Header().Set("X-Nextgo-Zone-Future", "private")
				case "trailer":
					w.Header().Set("Trailer", zonecontract.HeaderReleaseID)
				case "upgrade":
					w.Header().Set("Upgrade", "websocket")
				case "asset-cookie":
					w.Header().Add("Set-Cookie", "session=private")
				case "asset-cookie-trailer":
					w.Header().Set("Trailer", "Set-Cookie")
				case "known-length":
					w.Header().Set("Content-Length", "1000")
				}
				_, _ = io.WriteString(w, "backend private data")
			}))
			defer server.Close()
			ingress := ingressTest(t, ingressManifest(ingressArtifact(t, "one", "", "r1", server.URL)), ZoneIngressOptions{MaxResponseBytes: 64})
			path := "/"
			if strings.HasPrefix(kind, "asset-cookie") {
				path = "/_nextgo/releases/r1/assets/x"
			}
			response := ingressRequest(ingress, "GET", path)
			if response.Code != 502 || strings.Contains(response.Body.String(), "backend private") || len(response.Header().Values("Set-Cookie")) != 0 || response.Header().Get(zonecontract.HeaderReleaseID) != "" {
				t.Fatal("unverified backend output committed", response.Code, response.Header(), response.Body.String())
			}
		})
	}
}

func TestZoneIngressTLSHandshakeIsHTTP1AndPhysicalUntilUserHookReturns(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 1 {
			t.Error("upstream negotiated a replay-capable HTTP2 transport")
		}
		ingressIdentity(w, r)
		w.Header().Set("Trailer", "X-Nextgo-Final")
		_, _ = io.WriteString(w, "ok")
		w.Header().Set("X-Nextgo-Final", "complete")
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.NextProtos = []string{"h2", "http/1.1"}
	ingress := ingressTest(t, ingressManifest(ingressArtifact(t, "one", "", "r1", server.URL)), ZoneIngressOptions{Transport: transport})
	response := ingressRequest(ingress, "GET", "/")
	if response.Code != 200 || response.Body.String() != "ok" || response.Result().Trailer.Get("X-Nextgo-Final") != "complete" || transport.TLSClientConfig.NextProtos[0] != "h2" {
		t.Fatal("TLS/hop/public-trailer policy changed", response.Code, response.Header(), response.Result().Trailer)
	}
	entered, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(unblock) }) }
	defer release()
	transport = server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.VerifyConnection = func(tls.ConnectionState) error { close(entered); <-unblock; return nil }
	held := ingressTest(t, ingressManifest(ingressArtifact(t, "one", "", "r1", server.URL)), ZoneIngressOptions{Transport: transport, MaxInFlight: 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	first := httptest.NewRecorder()
	go func() {
		defer close(finished)
		held.ServeHTTP(first, httptest.NewRequest("GET", "http://site.test/", nil).WithContext(ctx))
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("TLS callback did not start")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("logical TLS cancellation stuck")
	}
	if got := ingressRequest(held, "GET", "/"); got.Code != 503 {
		t.Fatal("TLS callback outlived its admission", got.Code)
	}
	closing, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if err := held.Close(closing); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("close released blocking TLS callback", err)
	}
	stop()
	release()
	joined := make(chan error, 1)
	go func() { joined <- held.Close(context.Background()) }()
	select {
	case err := <-joined:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("settled TLS callback retained admission")
	}
}

func TestZoneIngressNeverRetriesCommittedUnsafeRequests(t *testing.T) {
	for _, body := range []string{"", "payload"} {
		for _, idempotency := range []bool{false, true} {
			t.Run(fmt.Sprintf("body=%t/key=%t", body != "", idempotency), func(t *testing.T) {
				var commits, connections atomic.Int32
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "GET" {
						ingressIdentity(w, r)
						_, _ = io.WriteString(w, "warmup")
						return
					}
					_, _ = io.Copy(io.Discard, r.Body)
					if r.ProtoMajor != 1 || !r.Close || (r.Header.Get("Idempotency-Key") != "") != idempotency {
						t.Error("unsafe transport policy lost")
					}
					commits.Add(1)
					connection, _, _ := w.(http.Hijacker).Hijack()
					_ = connection.Close()
				}))
				server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
					if state == http.StateNew {
						connections.Add(1)
					}
				}
				server.Start()
				defer server.Close()
				ingress := ingressTest(t, ingressManifest(ingressArtifact(t, "one", "", "r1", server.URL)), ZoneIngressOptions{})
				if got := ingressRequest(ingress, "GET", "/"); got.Code != 200 {
					t.Fatal(got.Code)
				}
				request := httptest.NewRequest("POST", "http://site.test/commit", strings.NewReader(body))
				if idempotency {
					request.Header.Set("Idempotency-Key", "application-key")
				}
				response := httptest.NewRecorder()
				ingress.ServeHTTP(response, request)
				if response.Code != 502 || commits.Load() != 1 || connections.Load() != 2 {
					t.Fatal("unsafe request replayed/reused connection", response.Code, commits.Load(), connections.Load())
				}
			})
		}
	}
}

func TestZoneIngressStreamingOverflowAndTruncationAbortHTTP(t *testing.T) {
	for _, kind := range []string{"overflow", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "truncated" {
					connection, buffer, _ := w.(http.Hijacker).Hijack()
					defer connection.Close()
					_, _ = fmt.Fprintf(buffer, "HTTP/1.1 200 OK\r\nContent-Length: 8\r\n%s: 1\r\n%s: one\r\n%s: r1\r\n%s: %s\r\n\r\nshort", zonecontract.HeaderProtocol, zonecontract.HeaderZoneID, zonecontract.HeaderReleaseID, zonecontract.HeaderTopology, strings.Repeat("c", 64))
					_ = buffer.Flush()
					return
				}
				ingressIdentity(w, r)
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				_, _ = io.WriteString(w, strings.Repeat("x", 100))
			}))
			defer server.Close()
			ingress := ingressTest(t, ingressManifest(ingressArtifact(t, "one", "", "r1", server.URL)), ZoneIngressOptions{MaxResponseBytes: 8})
			front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.Host = "site.test"; ingress.ServeHTTP(w, r) }))
			defer front.Close()
			response, err := http.Get(front.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != 200 || readErr == nil || response.ContentLength != -1 {
				t.Fatal("truncated proxy looked complete", response.StatusCode, readErr, response.ContentLength)
			}
		})
	}
}

func TestZoneIngressRequestBudgetHEADAndRedirectDoNotChangeTarget(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		ingressIdentity(w, r)
		if r.Method == "HEAD" {
			w.Header().Set("Content-Length", "1000000")
			return
		}
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "http://must-not-contact.invalid/")
			w.WriteHeader(302)
			return
		}
		w.Header().Set("Content-Length", "1000000")
		w.WriteHeader(304)
	}))
	defer server.Close()
	ingress := ingressTest(t, ingressManifest(ingressArtifact(t, "one", "", "r1", server.URL)), ZoneIngressOptions{MaxRequestBytes: 4, MaxResponseBytes: 8})
	for _, known := range []bool{true, false} {
		request := httptest.NewRequest("POST", "http://site.test/", strings.NewReader("12345"))
		if !known {
			request.ContentLength = -1
		}
		response := httptest.NewRecorder()
		ingress.ServeHTTP(response, request)
		if response.Code != 413 || calls.Load() != 0 {
			t.Fatal(response.Code, calls.Load())
		}
	}
	if got := ingressRequest(ingress, "HEAD", "/"); got.Code != 200 || got.Body.Len() != 0 || got.Header().Get("Content-Length") != "1000000" {
		t.Fatal(got.Code, got.Header())
	}
	if got := ingressRequest(ingress, "GET", "/"); got.Code != 304 || got.Body.Len() != 0 {
		t.Fatal(got.Code, got.Header())
	}
	if got := ingressRequest(ingress, "GET", "/redirect"); got.Code != 302 || got.Header().Get("Location") != "http://must-not-contact.invalid/" || calls.Load() != 3 {
		t.Fatal(got.Code, calls.Load())
	}
}

type ingressHeldBody struct {
	started, readDone, closeStarted, closeDone chan struct{}
	readOnce, closeOnce                        sync.Once
}

func (body *ingressHeldBody) Read([]byte) (int, error) {
	body.readOnce.Do(func() { close(body.started) })
	<-body.readDone
	return 0, io.EOF
}
func (body *ingressHeldBody) Close() error {
	body.closeOnce.Do(func() { close(body.closeStarted) })
	<-body.closeDone
	return nil
}

func TestZoneIngressCancellationRetainsPhysicalBodyAndDialAdmissions(t *testing.T) {
	for _, kind := range []string{"body", "dial"} {
		t.Run(kind, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
				close(started)
				<-release
				return nil, errors.New("dial finished")
			}
			ingress := ingressTest(t, ingressManifest(ingressArtifact(t, "one", "", "r1", "http://backend.invalid")), ZoneIngressOptions{Transport: transport, MaxInFlight: 1})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := httptest.NewRequest("POST", "http://site.test/", nil).WithContext(ctx)
			var body *ingressHeldBody
			if kind == "body" {
				body = &ingressHeldBody{started: started, readDone: release, closeStarted: make(chan struct{}), closeDone: make(chan struct{})}
				request.Body = body
				request.ContentLength = -1
			}
			response := httptest.NewRecorder()
			finished := make(chan struct{})
			go func() { defer close(finished); ingress.ServeHTTP(response, request) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("physical work not admitted")
			}
			cancel()
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("logical cancellation stuck")
			}
			if response.Code != 504 && response.Code != 408 {
				t.Fatal(response.Code)
			}
			if got := ingressRequest(ingress, "GET", "/"); got.Code != 503 {
				t.Fatal("logical cancellation released physical admission", got.Code)
			}
			closing, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
			if err := ingress.Close(closing); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("close forgot physical worker", err)
			}
			stop()
			close(release)
			if body != nil {
				close(body.closeDone)
			}
			joined := make(chan error, 1)
			go func() { joined <- ingress.Close(context.Background()) }()
			select {
			case err := <-joined:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("physical work did not release admission")
			}
		})
	}
}

func TestZoneIngressConnectionNominationsExcludeTrailers(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		_, _ = fmt.Fprintf(buffer, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nConnection: X-Discard, close\r\nTrailer: X-Discard, X-Public\r\n%s: 1\r\n%s: one\r\n%s: r1\r\n%s: %s\r\n\r\n2\r\nok\r\n0\r\nX-Discard: secret\r\nX-Public: public\r\n\r\n", zonecontract.HeaderProtocol, zonecontract.HeaderZoneID, zonecontract.HeaderReleaseID, zonecontract.HeaderTopology, strings.Repeat("c", 64))
		_ = buffer.Flush()
	}))
	defer backend.Close()
	ingress := ingressTest(t, ingressManifest(ingressArtifact(t, "one", "", "r1", backend.URL)), ZoneIngressOptions{})
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.Host = "site.test"; ingress.ServeHTTP(w, r) }))
	defer front.Close()
	response, err := http.Get(front.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "ok" || response.Trailer.Get("X-Discard") != "" || response.Trailer.Get("X-Public") != "public" {
		t.Fatal(string(body), err, response.Trailer)
	}
	if _, declared := response.Trailer["X-Discard"]; declared {
		t.Fatal("hop trailer was declared", response.Trailer)
	}
}

type ingressHeldWriter struct {
	*httptest.ResponseRecorder
	started, release chan struct{}
	once             sync.Once
	deadline         time.Time
	holdFlush        bool
}

func (writer *ingressHeldWriter) SetWriteDeadline(deadline time.Time) error {
	writer.deadline = deadline
	return nil // Deliberately uncooperative native extension: ignores the deadline.
}
func (writer *ingressHeldWriter) hold() {
	writer.once.Do(func() { close(writer.started) })
	<-writer.release
}
func (writer *ingressHeldWriter) Write(data []byte) (int, error) {
	if !writer.holdFlush {
		writer.hold()
	}
	return writer.ResponseRecorder.Write(data)
}
func (writer *ingressHeldWriter) Flush() {
	if writer.holdFlush {
		writer.hold()
	}
	writer.ResponseRecorder.Flush()
}

func TestZoneIngressDownstreamPhysicalCompletionRetainsAdmission(t *testing.T) {
	for _, holdFlush := range []bool{false, true} {
		t.Run(fmt.Sprint("flush=", holdFlush), func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ingressIdentity(w, r)
				_, _ = io.WriteString(w, "ok")
			}))
			defer backend.Close()
			ingress := ingressTest(t, ingressManifest(ingressArtifact(t, "one", "", "r1", backend.URL)), ZoneIngressOptions{Timeout: 40 * time.Millisecond, MaxInFlight: 1})
			writer := &ingressHeldWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), release: make(chan struct{}), holdFlush: holdFlush}
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				defer func() {
					if value := recover(); value != nil && value != http.ErrAbortHandler {
						t.Error(value)
					}
				}()
				ingress.ServeHTTP(writer, httptest.NewRequest("GET", "http://site.test/", nil))
			}()
			select {
			case <-writer.started:
			case <-time.After(time.Second):
				t.Fatal("downstream operation did not start")
			}
			// The deadline was installed before starting the blocked public write.
			if writer.deadline.IsZero() || time.Until(writer.deadline) > 40*time.Millisecond {
				t.Fatal("missing bounded downstream deadline", writer.deadline)
			}
			<-time.After(60 * time.Millisecond)
			if got := ingressRequest(ingress, "GET", "/"); got.Code != 503 {
				t.Fatal("blocked public writer released admission", got.Code)
			}
			closing, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			if err := ingress.Close(closing); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("close forgot downstream physical work", err)
			}
			cancel()
			close(writer.release)
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("public writer did not finish")
			}
			if err := ingress.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestZoneIngressRealSlowReaderHasBoundedWriteDeadline(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ingressIdentity(w, r)
		if r.URL.Path == "/tiny" {
			_, _ = io.WriteString(w, "ok")
			return
		}
		block := strings.Repeat("x", 32<<10)
		for index := 0; index < 1024; index++ {
			if _, err := io.WriteString(w, block); err != nil {
				return
			}
		}
	}))
	defer backend.Close()
	ingress := ingressTest(t, ingressManifest(ingressArtifact(t, "one", "", "r1", backend.URL)), ZoneIngressOptions{Timeout: 150 * time.Millisecond, MaxResponseBytes: 64 << 20, MaxInFlight: 1})
	finished := make(chan struct{})
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		r.Host = "site.test"
		ingress.ServeHTTP(w, r)
	}))
	defer front.Close()
	connection, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if tcp, ok := connection.(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(1024)
	}
	_, err = io.WriteString(connection, "GET / HTTP/1.1\r\nHost: site.test\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	// Never read the response: the real server socket eventually blocks in Write.
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("slow public reader outlived the ingress write budget")
	}
	deadline := time.Now().Add(time.Second)
	for {
		got := ingressRequest(ingress, "GET", "/tiny")
		if got.Code == 200 && got.Body.String() == "ok" {
			break
		}
		if got.Code != 503 || time.Now().After(deadline) {
			t.Fatal("completed slow reader retained admission", got.Code)
		}
		time.Sleep(time.Millisecond)
	}
}
