package worker_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/offline"
	"github.com/Suhaibinator/Next.go/offline/worker"
)

// The model stores bytes, not catalog observations. Every fetch/read computes
// its observation again, so a controller which trusts claims instead of bodies
// cannot pass the corruption and interrupted-publication scenarios below.
type modelBody struct {
	bytes                                                                             []byte
	mime, admission, document, runtime, configuration, generation, vary, cacheControl string
}

type modelCall struct {
	op   string
	args modelArgs
}
type modelArgs struct {
	Namespace, Token, Cache, Key, Text, URL, Source, Generation, RuntimeID, ConfigurationID string
	Max                                                                                     int64
	Keep, Document                                                                          bool
	Handle                                                                                  int
	Entry                                                                                   offline.Entry
}
type modelFailure struct {
	op, cache, keyPart string
	remaining          int
}
type modelBrowser struct {
	mu                    sync.Mutex
	gate                  chan struct{}
	caches                map[string]map[string]modelBody
	network               map[string]modelBody
	live                  map[string]modelBody
	catalog               string
	handles               map[int]modelBody
	nextHandle, nextToken int
	now                   int64
	clients, visible      []string
	calls                 []modelCall
	failures              []modelFailure
	networkWhileLocked    bool
}
type modelBackend struct {
	browser *modelBrowser
	active  bool
}

func newModelBrowser() *modelBrowser {
	b := &modelBrowser{gate: make(chan struct{}, 1), caches: map[string]map[string]modelBody{}, network: map[string]modelBody{}, live: map[string]modelBody{}, handles: map[int]modelBody{}, now: 1001}
	b.gate <- struct{}{}
	return b
}
func modelHash(value []byte) string            { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func cloneModelBody(value modelBody) modelBody { value.bytes = bytes.Clone(value.bytes); return value }
func modelJSON(value any) (json.RawMessage, error) {
	data, err := json.Marshal(value)
	return data, err
}

func (backend *modelBackend) Call(op string, raw any) (json.RawMessage, error) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var arg modelArgs
	if err = json.Unmarshal(encoded, &arg); err != nil {
		return nil, err
	}
	b := backend.browser
	if op == "lock" {
		<-b.gate
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, modelCall{op, arg})
	for i := range b.failures {
		f := &b.failures[i]
		if f.remaining > 0 && f.op == op && (f.cache == "" || f.cache == arg.Cache) && (f.keyPart == "" || strings.Contains(arg.Key, f.keyPart)) {
			f.remaining--
			if op == "put" {
				delete(b.handles, arg.Handle)
			} // Cache.put consumes its response lease even when the physical write fails.
			return nil, errors.New("model: physical operation failed")
		}
	}
	observe := func(value modelBody, url string, keep bool) (json.RawMessage, error) {
		if arg.Max > 0 && int64(len(value.bytes)) > arg.Max {
			return nil, errors.New("model: decoded body exceeds cap")
		}
		handle := 0
		if keep {
			b.nextHandle++
			handle = b.nextHandle
			b.handles[handle] = cloneModelBody(value)
		}
		return modelJSON(map[string]any{"found": true, "handle": handle, "observation": offline.Observation{URL: url, SHA256: modelHash(value.bytes), DecodedBytes: int64(len(value.bytes)), ContentType: value.mime}, "admission": value.admission, "document": value.document, "runtimeID": value.runtime, "configurationID": value.configuration, "generation": value.generation, "vary": value.vary, "cacheControl": value.cacheControl})
	}
	switch op {
	case "lock":
		b.nextToken++
		return modelJSON(map[string]any{"token": fmt.Sprintf("lock-%d", b.nextToken)})
	case "unlock":
		b.gate <- struct{}{}
		return modelJSON(struct{}{})
	case "role":
		return modelJSON(map[string]any{"active": backend.active})
	case "clock":
		return modelJSON(map[string]any{"now": b.now})
	case "network":
		if len(b.gate) == 0 {
			b.networkWhileLocked = true
		}
		value, found := b.live[arg.Token]
		if !found {
			return nil, errors.New("model: live transport unavailable")
		}
		b.nextHandle++
		b.handles[b.nextHandle] = cloneModelBody(value)
		return modelJSON(map[string]any{"handle": b.nextHandle})
	case "clients":
		return modelJSON(map[string]any{"controlled": len(b.clients), "clientIDs": append([]string{}, b.clients...), "visibleIDs": append([]string{}, b.visible...)})
	case "token":
		b.nextToken++
		return modelJSON(map[string]any{"token": fmt.Sprintf("stage-%d", b.nextToken)})
	case "list":
		names := []string{}
		for name := range b.caches {
			if name == arg.Namespace+"-meta" || strings.HasPrefix(name, arg.Namespace+"-g-") {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		return modelJSON(map[string]any{"names": names})
	case "create":
		if b.caches[arg.Cache] == nil {
			b.caches[arg.Cache] = map[string]modelBody{}
		}
		return modelJSON(struct{}{})
	case "remove":
		delete(b.caches, arg.Cache)
		return modelJSON(struct{}{})
	case "record":
		value, found := b.caches[arg.Cache][arg.Key]
		return modelJSON(map[string]any{"found": found, "text": string(value.bytes)})
	case "writeRecord":
		if b.caches[arg.Cache] == nil {
			b.caches[arg.Cache] = map[string]modelBody{}
		}
		b.caches[arg.Cache][arg.Key] = modelBody{bytes: []byte(arg.Text), mime: "application/json; charset=utf-8"}
		return modelJSON(struct{}{})
	case "keys":
		keys := []string{}
		for key := range b.caches[arg.Cache] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return modelJSON(map[string]any{"keys": keys})
	case "readEntry":
		value, found := b.caches[arg.Cache][arg.URL]
		if !found {
			return modelJSON(map[string]any{"found": false})
		}
		return observe(value, arg.URL, arg.Keep)
	case "fetchCatalog":
		return modelJSON(map[string]any{"text": b.catalog})
	case "fetchEntry":
		source := arg.Source
		if source == "" {
			source = arg.Entry.URL
		}
		value, found := b.network[source]
		if !found {
			return nil, errors.New("model: network body unavailable")
		}
		// The primitive hashes the bytes it actually received; it never copies
		// Entry.SHA256 or Entry.DecodedBytes into an observation.
		if int64(len(value.bytes)) > arg.Entry.DecodedBytes {
			return nil, errors.New("model: decoded body exceeds cap")
		}
		return observe(value, arg.Entry.URL, true)
	case "put":
		value, found := b.handles[arg.Handle]
		delete(b.handles, arg.Handle)
		if !found {
			return nil, errors.New("model: missing response handle")
		}
		if b.caches[arg.Cache] == nil {
			b.caches[arg.Cache] = map[string]modelBody{}
		}
		b.caches[arg.Cache][arg.URL] = modelBody{bytes: bytes.Clone(value.bytes), mime: value.mime, cacheControl: "no-store"}
		return modelJSON(struct{}{})
	case "release":
		delete(b.handles, arg.Handle)
		return modelJSON(struct{}{})
	default:
		return nil, fmt.Errorf("model: unimplemented browser primitive %q", op)
	}
}

type modelBundle struct {
	catalog    *offline.Catalog
	descriptor offline.WorkerDescriptor
	raw        []byte
	bodies     map[string]modelBody
}

func newModelBundle(t *testing.T, label string, budget offline.Budget) modelBundle {
	t.Helper()
	config := offline.Config{ID: "public-guide", Origin: "https://example.test", BasePath: "/guide", Semantics: offline.SavedAnonymousPublic, Pages: []offline.Page{{Path: "/", MaxAge: time.Hour}, {Path: "/offline", MaxAge: time.Hour}}, Fallback: "/offline", Budget: budget}
	runtime, configuration := modelHash([]byte("runtime:" + label))[:20], modelHash([]byte("configuration:"+label))
	prefix := config.Origin + config.BasePath + "/_nextgo/" + runtime + "/"
	bundle := modelBundle{bodies: map[string]modelBody{}}
	spec := offline.CatalogSpec{Version: offline.Version, Config: config, Admission: offline.NativeAdmissionV1, RuntimeID: runtime, Configuration: configuration, SavedAtUnix: 1000}
	for _, role := range []struct {
		kind       offline.AssetKind
		name, mime string
	}{{offline.WorkerWASM, "worker", "application/wasm"}, {offline.CoreWASM, "app.wasm", "application/wasm"}, {offline.RuntimeShim, "wasm_exec.js", "text/javascript; charset=utf-8"}, {offline.BootScript, "boot.js", "text/javascript; charset=utf-8"}, {offline.Style, "offline-public/style.css", "text/css; charset=utf-8"}, {offline.LazyWASM, "module", "application/wasm"}} {
		body := []byte(label + ":" + string(role.kind))
		sha := modelHash(body)
		name := role.name
		if role.kind == offline.WorkerWASM {
			name = "worker-" + sha + ".wasm"
		}
		if role.kind == offline.LazyWASM {
			name = "module-" + sha + ".wasm"
		}
		url := prefix + name
		spec.Assets = append(spec.Assets, offline.Asset{Kind: role.kind, URL: url, SHA256: sha, DecodedBytes: int64(len(body)), ContentType: role.mime})
		bundle.bodies[url] = modelBody{bytes: body, mime: role.mime}
	}
	policy, err := offline.Normalize(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range config.Pages {
		url, err := policy.DocumentURL(page.Path)
		if err != nil {
			t.Fatal(err)
		}
		body := []byte("<!doctype html><title>" + label + page.Path + "</title>")
		sha := modelHash(body)
		spec.Documents = append(spec.Documents, offline.Document{Path: page.Path, SHA256: sha, DecodedBytes: int64(len(body))})
		bundle.bodies[url] = modelBody{bytes: body, mime: "text/html; charset=utf-8", admission: offline.NativeAdmissionV1, document: sha, runtime: runtime, configuration: configuration, cacheControl: "no-store"}
	}
	bundle.catalog, err = offline.NewCatalog(spec)
	if err != nil {
		t.Fatal(err)
	}
	catalogJSON, err := json.Marshal(bundle.catalog.Spec())
	if err != nil {
		t.Fatal(err)
	}
	bundle.descriptor = offline.WorkerDescriptor{Version: offline.Version, Client: offline.ClientDescriptor{Version: offline.Version, Scope: policy.ScopeURL(), WorkerURL: config.Origin + config.BasePath + offline.WorkerScriptPath, CatalogURL: config.Origin + config.BasePath + offline.CatalogPath, OwnerPolicyID: config.ID, RuntimeID: runtime, ConfigurationID: configuration}, Generation: bundle.catalog.Generation(), CatalogJSON: string(catalogJSON), CatalogSHA256: modelHash(catalogJSON), Namespace: policy.StorageNamespace()}
	for url, value := range bundle.bodies {
		value.generation = bundle.catalog.Generation()
		bundle.bodies[url] = value
	}
	bundle.raw, err = json.Marshal(bundle.descriptor)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}
func (b modelBundle) engine(t *testing.T, browser *modelBrowser, active bool) (*worker.Engine, *modelBackend) {
	t.Helper()
	backend := &modelBackend{browser: browser, active: active}
	engine, err := worker.NewEngine(b.raw, backend)
	if err != nil {
		t.Fatal(err)
	}
	return engine, backend
}
func (b modelBundle) publish(browser *modelBrowser) {
	browser.mu.Lock()
	defer browser.mu.Unlock()
	browser.catalog = b.descriptor.CatalogJSON
	for url, value := range b.bodies {
		if value.admission != "" {
			for _, document := range b.catalog.Spec().Documents {
				canonical, _ := b.catalog.Policy().DocumentURL(document.Path)
				if canonical == url {
					source, err := b.catalog.PreparationURL(document.Path)
					if err != nil {
						panic(err)
					}
					browser.network[source] = cloneModelBody(value)
				}
			}
		} else {
			browser.network[url] = cloneModelBody(value)
		}
	}
}
func (b modelBundle) command(kind, id string) worker.Command {
	return worker.Command{Version: 1, Command: kind, OwnerPolicyID: b.descriptor.Client.OwnerPolicyID, RuntimeID: b.descriptor.Client.RuntimeID, ConfigurationID: b.descriptor.Client.ConfigurationID, RequestID: id}
}
func (b modelBundle) request() worker.Request {
	return worker.Request{URL: "https://example.test/guide", Method: "GET", Mode: "navigate", Destination: "document"}
}
func modelMessage(engine *worker.Engine, b modelBundle, kind string) worker.Status {
	return engine.Message(b.command(kind, "request-1"), "https://example.test/guide")
}
func requireModelReady(t *testing.T, status worker.Status, b modelBundle) {
	t.Helper()
	if !status.Enabled || status.State != "ready" || status.Error != "" || status.ActiveGeneration != b.catalog.Generation() {
		t.Fatalf("expected ready generation %s: %+v", b.catalog.Generation(), status)
	}
}
func enableModel(t *testing.T, browser *modelBrowser, b modelBundle) (*worker.Engine, *modelBackend) {
	t.Helper()
	b.publish(browser)
	engine, backend := b.engine(t, browser, true)
	requireModelReady(t, modelMessage(engine, b, "enable"), b)
	return engine, backend
}
func (b *modelBrowser) cachedBody(t *testing.T, decision worker.Decision) []byte {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if decision.Kind != "cached" || decision.Handle < 1 {
		t.Fatalf("expected cached response: %+v", decision)
	}
	value, found := b.handles[decision.Handle]
	delete(b.handles, decision.Handle)
	if !found {
		t.Fatal("cached decision did not own a live response handle")
	}
	return bytes.Clone(value.bytes)
}
func (b *modelBrowser) opCount(op string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, call := range b.calls {
		if call.op == op {
			n++
		}
	}
	return n
}
func (b *modelBrowser) index(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	for name, entries := range b.caches {
		if strings.HasSuffix(name, "-meta") {
			for _, body := range entries {
				var index map[string]json.RawMessage
				if json.Unmarshal(body.bytes, &index) == nil && index["version"] != nil {
					return index
				}
			}
		}
	}
	return nil
}
func indexString(index map[string]json.RawMessage, key string) string {
	var value string
	_ = json.Unmarshal(index[key], &value)
	return value
}
func indexBool(index map[string]json.RawMessage, key string) bool {
	var value bool
	_ = json.Unmarshal(index[key], &value)
	return value
}
func (b *modelBrowser) cacheName(generation string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for name := range b.caches {
		if strings.HasSuffix(name, "-g-"+generation) {
			return name
		}
	}
	return ""
}

func TestEngineRequiresConsentBeforeSavingCompleteInventory(t *testing.T) {
	browser := newModelBrowser()
	a := newModelBundle(t, "A", offline.Budget{})
	a.publish(browser)
	engine, _ := a.engine(t, browser, true)
	if err := engine.Lifecycle("install"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Lifecycle("activate"); err != nil {
		t.Fatal(err)
	}
	if browser.opCount("fetchEntry") != 0 || browser.opCount("writeRecord") != 0 {
		t.Fatal("registration alone saved data without enable consent")
	}
	status := modelMessage(engine, a, "status")
	if status.Enabled || status.State == "ready" {
		t.Fatalf("unsaved status: %+v", status)
	}
	requireModelReady(t, modelMessage(engine, a, "enable"), a)
	if browser.opCount("fetchEntry") != len(a.catalog.Entries()) {
		t.Fatal("enable did not fetch the complete required inventory")
	}
	if got := browser.cachedBody(t, engine.Fetch(a.request())); !bytes.Equal(got, a.bodies[a.request().URL].bytes) {
		t.Fatal("cached document is not the admitted decoded body")
	}
}

func TestEngineLiveNavigationForwardsWithoutSavingOrStorageLock(t *testing.T) {
	browser := newModelBrowser()
	a := newModelBundle(t, "A", offline.Budget{})
	engine, _ := a.engine(t, browser, false)
	live := []byte("live native response, not a saved anonymous snapshot")
	browser.live["fetch-event-1"] = modelBody{bytes: live, mime: "text/html; charset=utf-8", cacheControl: "private, no-store"}
	request := a.request()
	request.Token = "fetch-event-1"
	decision := engine.Fetch(request)
	if decision.Kind != "network" || decision.Saved || decision.Handle < 1 {
		t.Fatalf("live navigation was replaced or marked saved: %+v", decision)
	}
	browser.mu.Lock()
	response, found := browser.handles[decision.Handle]
	delete(browser.handles, decision.Handle)
	browser.mu.Unlock()
	if !found || !bytes.Equal(response.bytes, live) {
		t.Fatal("live native response handle was not preserved")
	}
	if browser.opCount("lock") != 0 || browser.opCount("fetchEntry") != 0 || len(browser.caches) != 0 {
		t.Fatal("live forwarding entered saved-storage lifecycle")
	}
}

func TestEngineWaitingInstallStagesAllOfBWithoutPromotingA(t *testing.T) {
	browser := newModelBrowser()
	a, b := newModelBundle(t, "A", offline.Budget{}), newModelBundle(t, "B", offline.Budget{})
	engineA, _ := enableModel(t, browser, a)
	b.publish(browser)
	engineB, backendB := b.engine(t, browser, false)
	before := browser.opCount("fetchEntry")
	if err := engineB.Lifecycle("install"); err != nil {
		t.Fatal(err)
	}
	index := browser.index(t)
	if indexString(index, "active") != a.catalog.Generation() || indexString(index, "staging") != b.catalog.Generation() || !indexBool(index, "stageComplete") {
		t.Fatalf("waiting install promoted or incompletely staged: %s", mustModelJSON(t, index))
	}
	if browser.opCount("fetchEntry")-before != len(b.catalog.Entries()) {
		t.Fatal("waiting install did not settle the full B inventory")
	}
	if got := browser.cachedBody(t, engineA.Fetch(a.request())); !bytes.Equal(got, a.bodies[a.request().URL].bytes) {
		t.Fatal("waiting B replaced A's saved document")
	}
	if err := engineB.Lifecycle("activate"); err != nil {
		t.Fatal(err)
	}
	if indexString(browser.index(t), "active") != a.catalog.Generation() {
		t.Fatal("waiting worker promoted itself before UA activation")
	}
	backendB.active = true
	if err := engineB.Lifecycle("activate"); err != nil {
		t.Fatal(err)
	}
	requireModelReady(t, modelMessage(engineB, b, "status"), b)
	if indexString(browser.index(t), "previous") != a.catalog.Generation() {
		t.Fatal("activation silently forgot retained A")
	}
}

func TestEngineFailedUpdateKeepsSavedAUsable(t *testing.T) {
	for _, fault := range []string{"network-failure", "put-failure", "hash-corruption", "missing-required-body", "wrong-native-generation", "application-vary", "private-asset"} {
		t.Run(fault, func(t *testing.T) {
			browser := newModelBrowser()
			a, b := newModelBundle(t, "A", offline.Budget{}), newModelBundle(t, "B", offline.Budget{})
			engineA, _ := enableModel(t, browser, a)
			b.publish(browser)
			asset := b.catalog.Spec().Assets[0]
			prepared, err := b.catalog.PreparationURL("/")
			if err != nil {
				t.Fatal(err)
			}
			browser.mu.Lock()
			switch fault {
			case "network-failure":
				browser.failures = append(browser.failures, modelFailure{op: "fetchEntry", remaining: 1})
			case "put-failure":
				browser.failures = append(browser.failures, modelFailure{op: "put", remaining: 1})
			case "hash-corruption":
				value := browser.network[asset.URL]
				value.bytes = bytes.Clone(value.bytes)
				value.bytes[0] ^= 1
				browser.network[asset.URL] = value
			case "missing-required-body":
				delete(browser.network, asset.URL)
			case "wrong-native-generation":
				value := browser.network[prepared]
				value.generation = a.catalog.Generation()
				browser.network[prepared] = value
			case "application-vary":
				value := browser.network[prepared]
				value.vary = "Cookie"
				browser.network[prepared] = value
			case "private-asset":
				value := browser.network[asset.URL]
				value.cacheControl = "private, no-store"
				browser.network[asset.URL] = value
			}
			browser.mu.Unlock()
			engineB, _ := b.engine(t, browser, false)
			if err := engineB.Lifecycle("install"); err == nil {
				t.Fatal("incomplete B install succeeded")
			}
			if indexString(browser.index(t), "active") != a.catalog.Generation() {
				t.Fatal("failed update changed active A")
			}
			if len(browser.handles) != 0 {
				t.Fatal("failed install retained an unconsumed response handle")
			}
			if got := browser.cachedBody(t, engineA.Fetch(a.request())); !bytes.Equal(got, a.bodies[a.request().URL].bytes) {
				t.Fatal("failed B install lost saved A")
			}
		})
	}
}

// This exercises decoded bodies and transport metadata, not actual gzip bytes.
// Native wrapper and browser compression have separate integration proof.
func TestEnginePreparedDeliveryVaryAllowsOnlyCompression(t *testing.T) {
	cases := []struct {
		name, vary string
		allowed    bool
	}{
		{"empty", "", true},
		{"accept-encoding", "Accept-Encoding", true},
		{"case-insensitive", "aCcEpT-EnCoDiNg", true},
		{"field-whitespace", " \tAccept-Encoding \t", true},
		{"cookie", "Cookie", false},
		{"authorization", "Authorization", false},
		{"duplicate-compression", "Accept-Encoding, accept-encoding", false},
		{"compression-and-cookie", "Accept-Encoding, Cookie", false},
		{"compression-and-authorization", "Accept-Encoding, Authorization", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			browser := newModelBrowser()
			a, b := newModelBundle(t, "A", offline.Budget{}), newModelBundle(t, "B", offline.Budget{})
			engineA, backendA := enableModel(t, browser, a)
			b.publish(browser)
			for _, document := range b.catalog.Spec().Documents {
				source, err := b.catalog.PreparationURL(document.Path)
				if err != nil {
					t.Fatal(err)
				}
				value, found := browser.network[source]
				if !found {
					t.Fatal("fixture omitted the dedicated native preparation source")
				}
				value.vary = test.vary
				browser.network[source] = value
			}
			engineB, backendB := b.engine(t, browser, false)
			err := engineB.Lifecycle("install")
			if !test.allowed {
				if err == nil {
					t.Fatal("application or duplicate Vary granted offline admission")
				}
				if indexString(browser.index(t), "active") != a.catalog.Generation() {
					t.Fatal("refused delivery replaced the prior active generation")
				}
				if got := browser.cachedBody(t, engineA.Fetch(a.request())); !bytes.Equal(got, a.bodies[a.request().URL].bytes) {
					t.Fatal("refused delivery lost intact saved A")
				}
				return
			}
			if err != nil {
				t.Fatalf("compression-only transport negotiation rejected: %v", err)
			}
			if indexString(browser.index(t), "active") != a.catalog.Generation() || !indexBool(browser.index(t), "stageComplete") {
				t.Fatal("waiting delivery did not retain A while completing B")
			}
			backendA.active, backendB.active = false, true
			if err := engineB.Lifecycle("activate"); err != nil {
				t.Fatal(err)
			}
			requireModelReady(t, modelMessage(engineB, b, "status"), b)
			if got := browser.cachedBody(t, engineB.Fetch(b.request())); !bytes.Equal(got, b.bodies[b.request().URL].bytes) {
				t.Fatal("transport negotiation changed the observed decoded body")
			}
		})
	}
}

func TestEngineExpiredOrRolledBackClockCannotBecomeReadyOrServe(t *testing.T) {
	browser := newModelBrowser()
	a := newModelBundle(t, "A", offline.Budget{})
	engine, _ := enableModel(t, browser, a)
	browser.now = 4600
	expired := modelMessage(engine, a, "status")
	if expired.Enabled || expired.State == "ready" {
		t.Fatalf("expired catalog claimed ready: %+v", expired)
	}
	if decision := engine.Fetch(a.request()); decision.Kind == "cached" {
		t.Fatalf("expired document served: %+v", decision)
	}
	browser.now = 4500
	rolled := modelMessage(engine, a, "status")
	if rolled.Enabled || rolled.State == "ready" {
		t.Fatalf("expired document revived after status-observed rollback: %+v", rolled)
	}
	if decision := engine.Fetch(a.request()); decision.Kind == "cached" {
		t.Fatalf("clock rollback served saved document: %+v", decision)
	}
}

func TestEngineInterruptedReservationRequiresOwnedPhysicalCleanup(t *testing.T) {
	browser := newModelBrowser()
	a, b := newModelBundle(t, "A", offline.Budget{}), newModelBundle(t, "B", offline.Budget{})
	engineA, backendA := enableModel(t, browser, a)
	b.publish(browser)
	browser.failures = append(browser.failures, modelFailure{op: "writeRecord", keyPart: "__nextgo_offline_catalog__", remaining: 1})
	engineB, _ := b.engine(t, browser, false)
	if err := engineB.Lifecycle("install"); err == nil {
		t.Fatal("interrupted catalog write succeeded")
	}
	index := browser.index(t)
	if indexString(index, "staging") != b.catalog.Generation() || indexString(index, "active") != a.catalog.Generation() {
		t.Fatal("interruption lost its durable reservation or last-good generation")
	}
	stageName := browser.cacheName(b.catalog.Generation())
	if stageName == "" {
		t.Fatal("test did not interrupt after creating physical staging")
	}
	browser.failures = append(browser.failures, modelFailure{op: "remove", cache: stageName, remaining: 1})
	beforeFetch := browser.opCount("fetchEntry")
	restarted, backendB := b.engine(t, browser, false)
	if err := restarted.Lifecycle("install"); err == nil || browser.opCount("fetchEntry") != beforeFetch {
		t.Fatal("new writes bypassed unsettled staging removal")
	}
	if indexString(browser.index(t), "staging") != b.catalog.Generation() || browser.cacheName(b.catalog.Generation()) == "" {
		t.Fatal("failed physical cleanup released the persisted reservation")
	}
	if got := browser.cachedBody(t, engineA.Fetch(a.request())); !bytes.Equal(got, a.bodies[a.request().URL].bytes) {
		t.Fatal("active A could not recover its intact saved body after incomplete B cleanup")
	}
	if indexString(browser.index(t), "staging") != "" || browser.cacheName(b.catalog.Generation()) != "" {
		t.Fatal("A recovery did not settle exact interrupted B cleanup")
	}
	if err := restarted.Lifecycle("install"); err != nil {
		t.Fatal(err)
	}
	backendA.active = false
	backendB.active = true
	if err := restarted.Lifecycle("activate"); err != nil {
		t.Fatal(err)
	}
	requireModelReady(t, modelMessage(restarted, b, "status"), b)
	if browser.opCount("remove") == 0 {
		t.Fatal("interrupted namespace was reused without physical deletion")
	}
}

func TestEngineMissingOrCorruptStoredRoleFailsClosed(t *testing.T) {
	for _, fault := range []string{"missing-worker", "corrupt-core", "corrupt-document"} {
		t.Run(fault, func(t *testing.T) {
			browser := newModelBrowser()
			a := newModelBundle(t, "A", offline.Budget{})
			engine, _ := enableModel(t, browser, a)
			name := browser.cacheName(a.catalog.Generation())
			var url string
			for _, entry := range a.catalog.Entries() {
				if fault == "missing-worker" && entry.Kind == string(offline.WorkerWASM) || fault == "corrupt-core" && entry.Kind == string(offline.CoreWASM) || fault == "corrupt-document" && entry.Kind == "document" {
					url = entry.URL
					break
				}
			}
			if url == "" {
				t.Fatal("fixture role missing")
			}
			browser.mu.Lock()
			if fault == "missing-worker" {
				delete(browser.caches[name], url)
			} else {
				value := browser.caches[name][url]
				value.bytes = bytes.Clone(value.bytes)
				value.bytes[0] ^= 1
				browser.caches[name][url] = value
			}
			browser.mu.Unlock()
			if status := modelMessage(engine, a, "status"); status.Enabled || status.State == "ready" {
				t.Fatalf("corrupt required inventory claimed ready: %+v", status)
			}
			before := len(browser.handles)
			if decision := engine.Fetch(a.request()); decision.Kind == "cached" {
				t.Fatalf("corrupt required inventory served: %+v", decision)
			}
			if len(browser.handles) != before {
				t.Fatal("failed inventory verification leaked a response handle")
			}
		})
	}
}

func TestEngineStatusReportsActualWorkerIdentityBeforeCrossGenerationCommand(t *testing.T) {
	browser := newModelBrowser()
	a, b := newModelBundle(t, "A", offline.Budget{}), newModelBundle(t, "B", offline.Budget{})
	_, _ = enableModel(t, browser, a)
	b.publish(browser)
	engineB, _ := b.engine(t, browser, true)
	status := engineB.Message(a.command("status", "handshake-1"), "https://example.test/guide")
	if status.RequestID != "handshake-1" || status.RuntimeID != b.descriptor.Client.RuntimeID || status.ConfigurationID != b.descriptor.Client.ConfigurationID || status.Generation != b.catalog.Generation() || status.ActiveGeneration != a.catalog.Generation() {
		t.Fatalf("handshake copied caller identity instead of actual worker: %+v", status)
	}
	before := browser.opCount("writeRecord")
	refused := engineB.Message(a.command("enable", "old-command"), "https://example.test/guide")
	if refused.Error != "identity-changed" || browser.opCount("writeRecord") != before {
		t.Fatalf("old document changed a different worker generation: %+v", refused)
	}
	for _, source := range []string{"https://foreign.test/guide", "https://example.test/guide-peer", "https://user@example.test/guide"} {
		if got := engineB.Message(b.command("enable", "source-check"), source); got.Error != "invalid-message" {
			t.Fatalf("foreign source admitted: %s %+v", source, got)
		}
	}
}

func TestEngineUnsafeRequestsNeverConsultSavedStorage(t *testing.T) {
	browser := newModelBrowser()
	a := newModelBundle(t, "A", offline.Budget{})
	engine, _ := enableModel(t, browser, a)
	cases := map[string]func(*worker.Request){"POST": func(r *worker.Request) { r.Method = "POST" }, "authorization": func(r *worker.Request) { r.Authorization = true }, "cookie": func(r *worker.Request) { r.Cookie = true }, "SPA": func(r *worker.Request) { r.SPA = true }, "range": func(r *worker.Request) { r.Range = true }, "no-store": func(r *worker.Request) { r.Cache = "no-store" }, "query": func(r *worker.Request) { r.URL += "?private=1" }, "API": func(r *worker.Request) { r.URL = "https://example.test/guide/api/account" }, "peer": func(r *worker.Request) { r.URL = "https://example.test/guide-peer" }, "foreign-origin": func(r *worker.Request) { r.URL = "https://foreign.test/guide" }, "document-fetch": func(r *worker.Request) { r.Mode = "cors" }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			request := a.request()
			change(&request)
			before := len(browser.calls)
			decision := engine.Fetch(request)
			if decision.Kind != "pass" || len(browser.calls) != before {
				t.Fatalf("unsafe request consulted saved data: %+v %+v", request, decision)
			}
		})
	}
}

func TestEngineReloadAndNoCacheUseNetworkThenVerifiedSavedFallback(t *testing.T) {
	for _, mode := range []string{"reload", "no-cache"} {
		t.Run(mode, func(t *testing.T) {
			browser := newModelBrowser()
			a := newModelBundle(t, "A", offline.Budget{})
			engine, _ := enableModel(t, browser, a)
			request := a.request()
			request.Cache = mode
			request.Token = "navigation-event"
			beforeNetwork, beforePut := browser.opCount("network"), browser.opCount("put")
			if got := browser.cachedBody(t, engine.Fetch(request)); !bytes.Equal(got, a.bodies[request.URL].bytes) {
				t.Fatal("offline user reload lost the verified saved document")
			}
			if browser.opCount("network") != beforeNetwork+1 || browser.opCount("put") != beforePut {
				t.Fatal("reload did not try live request exactly once or persisted fallback")
			}
			live := []byte("fresh private native response")
			browser.live[request.Token] = modelBody{bytes: live, mime: "text/html; charset=utf-8", cacheControl: "private, no-store"}
			decision := engine.Fetch(request)
			if decision.Kind != "network" || decision.Saved {
				t.Fatalf("online reload did not retain live semantics: %+v", decision)
			}
			browser.mu.Lock()
			body, found := browser.handles[decision.Handle]
			delete(browser.handles, decision.Handle)
			browser.mu.Unlock()
			if !found || !bytes.Equal(body.bytes, live) || browser.opCount("put") != beforePut {
				t.Fatal("live reload response was lost or persisted")
			}
			if browser.networkWhileLocked {
				t.Fatal("live forwarding held the saved-storage lock")
			}
		})
	}
}

func TestEngineCorruptOrExpiredAssetRecoversOnlineWithoutRepairingSavedPack(t *testing.T) {
	for _, fault := range []string{"corrupt", "expired"} {
		t.Run(fault, func(t *testing.T) {
			browser := newModelBrowser()
			a := newModelBundle(t, "A", offline.Budget{})
			engine, _ := enableModel(t, browser, a)
			var asset offline.Asset
			for _, entry := range a.catalog.Spec().Assets {
				if entry.Kind == offline.CoreWASM {
					asset = entry
					break
				}
			}
			if asset.URL == "" {
				t.Fatal("missing fixture core")
			}
			cache := browser.cacheName(a.catalog.Generation())
			if fault == "corrupt" {
				value := browser.caches[cache][asset.URL]
				value.bytes = bytes.Clone(value.bytes)
				value.bytes[0] ^= 1
				browser.caches[cache][asset.URL] = value
			} else {
				browser.now = 4600
			}
			storedBefore := bytes.Clone(browser.caches[cache][asset.URL].bytes)
			browser.live["asset-event"] = cloneModelBody(a.bodies[asset.URL])
			beforePut, beforeNetwork := browser.opCount("put"), browser.opCount("network")
			decision := engine.Fetch(worker.Request{URL: asset.URL, Method: "GET", Mode: "no-cors", Destination: "script", Token: "asset-event"})
			if decision.Kind != "network" || decision.Saved {
				t.Fatalf("damaged saved pack blocked online asset recovery: %+v", decision)
			}
			browser.mu.Lock()
			body, found := browser.handles[decision.Handle]
			delete(browser.handles, decision.Handle)
			browser.mu.Unlock()
			if !found || !bytes.Equal(body.bytes, a.bodies[asset.URL].bytes) {
				t.Fatal("actual live asset response was not forwarded")
			}
			if browser.opCount("network") != beforeNetwork+1 || browser.opCount("put") != beforePut || !bytes.Equal(browser.caches[cache][asset.URL].bytes, storedBefore) {
				t.Fatal("online recovery mixed bytes into invalid saved generation")
			}
			if browser.networkWhileLocked {
				t.Fatal("asset network recovery retained the storage lock")
			}
		})
	}
}

func TestEngineCorruptActiveCanDisableWithoutDeletingRetainedClients(t *testing.T) {
	browser := newModelBrowser()
	a := newModelBundle(t, "A", offline.Budget{})
	engine, _ := enableModel(t, browser, a)
	cache := browser.cacheName(a.catalog.Generation())
	url := a.request().URL
	value := browser.caches[cache][url]
	value.bytes = bytes.Clone(value.bytes)
	value.bytes[0] ^= 1
	browser.caches[cache][url] = value
	browser.clients = []string{"retained-one", "retained-two"}
	browser.visible = []string{"retained-one", "retained-two"}
	beforeRemove := browser.opCount("remove")
	status := modelMessage(engine, a, "disable")
	if status.Enabled || status.State != "disabled" || !status.DeferredCleanup || status.Error != "" {
		t.Fatalf("corruption trapped explicit disable: %+v", status)
	}
	if browser.opCount("remove") != beforeRemove || !indexBool(browser.index(t), "disabled") {
		t.Fatal("corrupt active pack was deleted or disable state was not durable")
	}
	if decision := engine.Fetch(a.request()); decision.Kind != "pass" {
		t.Fatalf("disabled corrupt document did not return native handling: %+v", decision)
	}
	browser.clients = nil
	browser.visible = []string{"retained-two"}
	if status = modelMessage(engine, a, "disable"); !status.DeferredCleanup || browser.opCount("remove") != beforeRemove {
		t.Fatalf("unregister lost an old retained client: %+v", status)
	}
	browser.visible = nil
	if status = modelMessage(engine, a, "disable"); status.Error != "" || status.DeferredCleanup || len(browser.caches) != 0 {
		t.Fatalf("closed retained clients could not repair corrupt storage: %+v", status)
	}
}

func TestEngineDisableRetainsEveryOldClientAcrossUnregister(t *testing.T) {
	browser := newModelBrowser()
	a := newModelBundle(t, "A", offline.Budget{})
	engine, _ := enableModel(t, browser, a)
	browser.clients = []string{"old-one", "old-two"}
	browser.visible = []string{"old-one", "old-two"}
	status := modelMessage(engine, a, "disable")
	if status.Enabled || status.State != "disabled" || !status.DeferredCleanup {
		t.Fatalf("disable ignored retained clients: %+v", status)
	}
	asset := a.catalog.Spec().Assets[0]
	retainedAsset := worker.Request{URL: asset.URL, Method: "GET", Mode: "no-cors", Destination: "script", ClientID: "old-one"}
	if got := browser.cachedBody(t, engine.Fetch(retainedAsset)); !bytes.Equal(got, a.bodies[asset.URL].bytes) {
		t.Fatal("disable broke a still-retained client's runtime")
	}
	retainedAsset.ClientID = "new-client"
	if decision := engine.Fetch(retainedAsset); decision.Kind == "cached" {
		t.Fatal("disabled retained assets were given to a new client")
	}
	before := browser.opCount("remove")
	browser.clients = nil // unregister no longer reports controlled clients; old documents remain visible.
	for _, visible := range [][]string{{"old-one", "old-two"}, {"old-two"}} {
		browser.visible = visible
		status = modelMessage(engine, a, "disable")
		if !status.DeferredCleanup || browser.opCount("remove") != before {
			t.Fatalf("cleanup ran while an old client remained: %+v", status)
		}
	}
	if decision := engine.Fetch(a.request()); decision.Kind == "cached" {
		t.Fatal("disabled worker served saved navigation")
	}
	browser.visible = nil
	status = modelMessage(engine, a, "disable")
	if status.DeferredCleanup || status.Enabled || len(browser.caches) != 0 {
		t.Fatalf("closed clients did not permit owned cleanup: %+v", status)
	}
}

func TestEnginePhysicalDeleteFailurePreservesIndexAndReservation(t *testing.T) {
	browser := newModelBrowser()
	a := newModelBundle(t, "A", offline.Budget{})
	engine, _ := enableModel(t, browser, a)
	before := browser.index(t)
	name := browser.cacheName(a.catalog.Generation())
	browser.failures = append(browser.failures, modelFailure{op: "remove", cache: name, remaining: 1})
	status := modelMessage(engine, a, "disable")
	if status.Error != "cleanup-failed" || !status.DeferredCleanup {
		t.Fatalf("physical deletion failure hidden: %+v", status)
	}
	after := browser.index(t)
	for _, field := range []string{"policy", "active", "previous", "staging", "stageToken"} {
		if indexString(after, field) != indexString(before, field) {
			t.Fatalf("failed physical deletion changed reserved ownership %s", field)
		}
	}
	if after == nil || browser.cacheName(a.catalog.Generation()) == "" {
		t.Fatal("failed physical deletion released index or payload reservation")
	}
	if got := modelMessage(engine, a, "disable"); got.Error != "" || len(browser.caches) != 0 {
		t.Fatalf("settled retry did not finish owned deletion: %+v", got)
	}
}

func TestEngineOwnedCleanupLeavesUnrelatedCachesUntouched(t *testing.T) {
	browser := newModelBrowser()
	a := newModelBundle(t, "A", offline.Budget{})
	engine, _ := enableModel(t, browser, a)
	foreign := []byte("unrelated owner's bytes")
	browser.caches["another-application-cache"] = map[string]modelBody{"https://example.test/other": {bytes: foreign, mime: "text/plain"}}
	if status := modelMessage(engine, a, "disable"); status.Error != "" || status.DeferredCleanup {
		t.Fatalf("owned cleanup failed: %+v", status)
	}
	if len(browser.caches) != 1 || !bytes.Equal(browser.caches["another-application-cache"]["https://example.test/other"].bytes, foreign) {
		t.Fatal("cleanup deleted another cache owner's data")
	}
}

func TestEngineQuotaRefusalLeavesActivePreviousAndIndexUnchanged(t *testing.T) {
	budget := offline.Budget{TotalBytes: 160 << 10, GenerationBytes: 80 << 10, AssetBytes: 1024, DocumentBytes: 1024}
	browser := newModelBrowser()
	a, b, c := newModelBundle(t, "A", budget), newModelBundle(t, "B", budget), newModelBundle(t, "C", budget)
	_, _ = enableModel(t, browser, a)
	b.publish(browser)
	engineB, backendB := b.engine(t, browser, false)
	if err := engineB.Lifecycle("install"); err != nil {
		t.Fatal(err)
	}
	backendB.active = true
	if err := engineB.Lifecycle("activate"); err != nil {
		t.Fatal(err)
	}
	before := mustModelJSON(t, browser.index(t))
	beforeFetch := browser.opCount("fetchEntry")
	c.publish(browser)
	engineC, _ := c.engine(t, browser, false)
	if err := engineC.Lifecycle("install"); err == nil {
		t.Fatal("three generation payloads exceeded explicit total without refusal")
	}
	if got := mustModelJSON(t, browser.index(t)); got != before || browser.opCount("fetchEntry") != beforeFetch {
		t.Fatal("quota refusal changed index or began payload writes")
	}
	if got := browser.cachedBody(t, engineB.Fetch(b.request())); !bytes.Equal(got, b.bodies[b.request().URL].bytes) {
		t.Fatal("quota refusal lost current saved B")
	}
}

func mustModelJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
