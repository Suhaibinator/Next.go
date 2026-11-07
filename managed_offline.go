//go:build !js

package nextgo

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Suhaibinator/Next.go/httpx"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/offline"
)

const (
	offlineGenerationHeader = "X-Nextgo-Offline-Generation"
	offlineAdmissionHeader  = "X-Nextgo-Offline-Admission"
	offlineDocumentHeader   = "X-Nextgo-Offline-Document"
)

type offlinePrepareOptions struct{ bundle *managedOffline }
type managedOfflineFile struct {
	body                []byte
	contentType, digest string
}
type managedOfflineBundle struct {
	catalog             *offline.Catalog
	catalogJSON, script []byte
	documents           map[string]managedOfflineFile
}
type managedOffline struct {
	policy    *offline.Policy
	build     offline.Build
	identity  string
	files     map[string]managedOfflineFile
	published atomic.Pointer[managedOfflineBundle]
	mu        sync.Mutex
	preparing chan struct{}
}

func freezeManagedOfflineConfig(c *Config) error {
	if c.Offline == nil && c.OfflineBuild == nil {
		return nil
	}
	if c.Offline == nil || c.OfflineBuild == nil {
		return fmt.Errorf("nextgo: managed offline requires both policy and compiler inventory")
	}
	if c.Dev || c.Phase != PhaseProduction || c.PrerenderBuildMode || c.Draft != nil || c.Locales != nil || c.localeScope != nil || c.Zone().ID != "" || c.AssetPrefix != "" || c.TrailingSlash || len(c.EarlyScripts) != 0 || len(c.Rewrites) != 0 || len(c.Redirects) != 0 || c.Images != nil || c.metadataSet.Digest() != "" {
		return fmt.Errorf("nextgo: unsupported managed offline configuration")
	}
	policy, err := offline.Normalize(*c.Offline)
	if err != nil {
		return fmt.Errorf("nextgo: invalid managed offline policy")
	}
	build, err := offline.NormalizeBuild(*c.OfflineBuild)
	if err != nil || build.RuntimeID != c.BuildID {
		return fmt.Errorf("nextgo: invalid managed offline compiler inventory")
	}
	config := policy.Config()
	if config.BasePath != c.BasePath || c.publicOrigin.String() != "" && c.publicOrigin.String() != config.Origin {
		return fmt.Errorf("nextgo: managed offline deployment identity differs")
	}
	if _, err := build.Assets(policy); err != nil {
		return fmt.Errorf("nextgo: managed offline inventory exceeds policy")
	}
	c.Offline, c.OfflineBuild = &config, &build
	return nil
}

func (a *App) initManagedOffline() error {
	if a.config.Offline == nil {
		return nil
	}
	policy, err := offline.Normalize(*a.config.Offline)
	if err != nil {
		return err
	}
	build := a.config.OfflineBuild.Clone()
	config := policy.Config()
	for _, page := range config.Pages {
		for path := range a.config.Handlers {
			if path == page.Path || path == config.BasePath+page.Path || strings.HasSuffix(path, "/") && (strings.HasPrefix(page.Path, path) || strings.HasPrefix(config.BasePath+page.Path, path)) {
				return fmt.Errorf("nextgo: managed offline page conflicts with an HTTP handler")
			}
		}
		found := false
		for _, route := range a.routes {
			if _, ok := match(route.Pattern, page.Path); !ok {
				continue
			}
			if !a.offlineRoutePublic(route) {
				return fmt.Errorf("nextgo: managed offline page requires an ordinary active public cache policy")
			}
			found = true
			break
		}
		if !found {
			return fmt.Errorf("nextgo: managed offline page is not a declared route")
		}
	}
	// This service namespace is exclusively owned, even when a page is dynamic.
	for _, route := range a.routes {
		if strings.HasPrefix(route.Pattern, "/_nextgo/offline/") || route.Pattern == "/_nextgo/offline" {
			return fmt.Errorf("nextgo: offline service conflicts with a route")
		}
		if _, ok := match(route.Pattern, offline.PreparedDocumentsPath+strings.Repeat("a", 64)+"/"+strings.Repeat("b", 64)); ok {
			return fmt.Errorf("nextgo: offline document service conflicts with a route")
		}
		if _, ok := match(route.Pattern, offline.CatalogPath); ok {
			return fmt.Errorf("nextgo: offline service conflicts with a route")
		}
		if _, ok := match(route.Pattern, offline.WorkerScriptPath); ok {
			return fmt.Errorf("nextgo: offline service conflicts with a route")
		}
		if _, ok := match(route.Pattern, offline.RegistrationHelperPath); ok {
			return fmt.Errorf("nextgo: offline helper conflicts with a route")
		}
	}
	for path := range a.config.Handlers {
		if strings.HasPrefix(path, "/_nextgo/offline/") || path == "/_nextgo/offline" || strings.HasSuffix(path, "/") && (strings.HasPrefix(offline.CatalogPath, path) || strings.HasPrefix(offline.WorkerScriptPath, path)) {
			return fmt.Errorf("nextgo: offline service conflicts with a handler")
		}
	}
	state := &managedOffline{policy: policy, build: build, files: make(map[string]managedOfflineFile, len(build.Files))}
	if a.config.Assets == nil {
		return fmt.Errorf("nextgo: managed offline requires compiler assets")
	}
	for _, file := range build.Files {
		if err := a.config.startupContext.Err(); err != nil {
			return err
		}
		input, err := a.config.Assets.Open(file.Path)
		if err != nil {
			return fmt.Errorf("nextgo: managed offline inventory file is unavailable")
		}
		info, statErr := input.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() != file.DecodedBytes {
			_ = input.Close()
			return fmt.Errorf("nextgo: managed offline inventory file shape differs")
		}
		body, readErr := io.ReadAll(io.LimitReader(input, file.DecodedBytes+1))
		closeErr := input.Close()
		if readErr != nil || closeErr != nil || int64(len(body)) != file.DecodedBytes || offlineDigest(body) != file.SHA256 {
			return fmt.Errorf("nextgo: managed offline inventory bytes differ")
		}
		if file.Kind == offline.CoreWASM || file.Kind == offline.WorkerWASM || file.Kind == offline.LazyWASM {
			if len(body) < 8 || !bytes.Equal(body[:8], []byte{'\x00', 'a', 's', 'm', 1, 0, 0, 0}) {
				return fmt.Errorf("nextgo: managed offline inventory has invalid WASM header")
			}
		}
		state.files[config.BasePath+"/"+file.Path] = managedOfflineFile{body: bytes.Clone(body), contentType: strings.Clone(file.ContentType), digest: strings.Clone(file.SHA256)}
	}
	if _, ok := state.files[config.BasePath+"/_nextgo/"+build.RuntimeID+"/offline-public/assets/style.css"]; !ok {
		return fmt.Errorf("nextgo: managed offline requires immutable style.css")
	}
	encoded, err := json.Marshal(struct {
		Domain string
		Policy offline.Config
		Build  offline.Build
	}{"nextgo.offline.config.v1", config, build})
	if err != nil {
		return err
	}
	state.identity = offlineDigest(encoded)
	a.managedOffline = state
	return nil
}

func (a *App) offlineConfigurationPolicy(previous string) string {
	if a.managedOffline == nil {
		return previous
	}
	return offlineDigest([]byte("nextgo.offline.configuration.v1\x00" + previous + "\x00" + a.managedOffline.identity))
}
func (a *App) offlineEndpointPatterns() []string {
	if a.managedOffline == nil {
		return nil
	}
	return []string{"/_nextgo/offline", "/_nextgo/offline/"}
}
func offlineDigest(body []byte) string { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }

func (a *App) offlineClientDescriptor() (offline.ClientDescriptor, error) {
	if a.managedOffline == nil {
		return offline.ClientDescriptor{}, offline.ErrInvalid
	}
	policy := a.managedOffline.policy
	config := policy.Config()
	base := config.Origin + config.BasePath
	return offline.NormalizeClientDescriptor(offline.ClientDescriptor{Version: offline.Version, Scope: policy.ScopeURL(), WorkerURL: base + offline.WorkerScriptPath, CatalogURL: base + offline.CatalogPath, OwnerPolicyID: config.ID, RuntimeID: a.config.BuildID, ConfigurationID: a.configurationID}, policy, a.config.BuildID, a.configurationID)
}
func (a *App) offlineDescriptorJSON() string {
	if a.managedOffline == nil {
		return ""
	}
	descriptor, err := a.offlineClientDescriptor()
	if err != nil {
		return ""
	}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return ""
	}
	return string(encoded)
}
func (a *App) offlineSavedDocument(r *http.Request) bool {
	capture := offlineCaptureFor(r)
	return capture != nil && capture.app == a && capture.options != nil && capture.options.bundle == a.managedOffline && !capture.fallback
}
func (a *App) offlineRenderPaths(ctx context.Context) paths.Config {
	capture, _ := ctx.Value(offlineCaptureKey{}).(*offlineCapture)
	if capture == nil || capture.app != a || capture.options == nil || capture.options.bundle != a.managedOffline {
		return a.paths
	}
	return paths.Config{BasePath: a.paths.BasePath, AssetPrefix: a.paths.BasePath + "/_nextgo/" + a.config.BuildID + "/offline-public"}
}
func (a *App) offlineStylesheetURL(r *http.Request) string {
	if !a.offlineSavedDocument(r) {
		return mustPath(a.paths.AssetURL("/style.css"))
	}
	value, _, err := managedOfflineAssetURL(r.Context(), "/style.css")
	if err != nil {
		panic(err)
	}
	return value
}
func managedOfflineAssetURL(ctx context.Context, local string) (string, bool, error) {
	if wrapped, ok := ctx.(*Context); ok {
		if wrapped == nil {
			return "", false, nil
		}
		ctx = wrapped.Context
	}
	if ctx == nil {
		return "", false, nil
	}
	capture, _ := ctx.Value(offlineCaptureKey{}).(*offlineCapture)
	if capture == nil || capture.options == nil {
		return "", false, nil
	}
	state := capture.options.bundle
	if state == nil || state != capture.app.managedOffline {
		capture.err = ErrOfflineAdmission
		return "", true, ErrOfflineAdmission
	}
	value, err := capture.app.offlineRenderPaths(ctx).AssetURL(local)
	if err != nil {
		capture.err = ErrOfflineAdmission
		return "", true, ErrOfflineAdmission
	}
	if _, ok := state.files[value]; !ok {
		capture.err = ErrOfflineAdmission
		return "", true, ErrOfflineAdmission
	}
	return value, true, nil
}

// PrepareOfflineBundle prepares once, before serving traffic. A complete frozen
// catalog, its exact documents and worker script become visible atomically.
// Failure/cancellation publishes nothing; later explicit retry is allowed.
func (a *App) PrepareOfflineBundle(ctx context.Context) error {
	if a == nil || ctx == nil {
		return ErrOfflineAdmission
	}
	if a.managedOffline == nil {
		return nil
	}
	state := a.managedOffline
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		state.mu.Lock()
		if state.published.Load() != nil {
			state.mu.Unlock()
			return nil
		}
		if state.preparing != nil {
			wait := state.preparing
			state.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		state.preparing = make(chan struct{})
		state.mu.Unlock()
		break
	}
	defer func() { state.mu.Lock(); close(state.preparing); state.preparing = nil; state.mu.Unlock() }()
	prepared, err := a.prepareOffline(ctx, state.policy.Config(), &offlinePrepareOptions{bundle: state})
	if err != nil {
		return err
	}
	assets, err := state.build.Assets(state.policy)
	if err != nil {
		return err
	}
	documents := make([]offline.Document, 0, len(prepared.documents))
	files := make(map[string]managedOfflineFile, len(prepared.documents))
	for _, document := range prepared.Documents() {
		if !document.Fallback {
			if err := a.validateManagedOfflineHTML(document.HTML); err != nil {
				return err
			}
		}
		documents = append(documents, offline.Document{Path: document.Path, SHA256: document.SHA256, DecodedBytes: int64(len(document.HTML))})
		if previous, exists := files[document.SHA256]; exists && !bytes.Equal(previous.body, document.HTML) {
			return ErrOfflineAdmission
		}
		files[document.SHA256] = managedOfflineFile{body: document.HTML, contentType: document.ContentType, digest: document.SHA256}
	}
	catalog, err := offline.NewCatalog(offline.CatalogSpec{Version: offline.Version, Config: state.policy.Config(), Admission: offline.NativeAdmissionV1, RuntimeID: a.config.BuildID, Configuration: a.configurationID, SavedAtUnix: time.Now().Unix(), Assets: assets, Documents: documents})
	if err != nil {
		return err
	}
	catalogJSON, err := offline.MarshalCatalog(catalog)
	if err != nil {
		return err
	}
	client, err := a.offlineClientDescriptor()
	if err != nil {
		return err
	}
	bootstrap, err := offline.NormalizeWorkerDescriptor(offline.WorkerDescriptor{Version: offline.Version, Client: client, Generation: catalog.Generation(), CatalogJSON: string(catalogJSON), CatalogSHA256: offlineDigest(catalogJSON), Namespace: state.policy.StorageNamespace()}, state.policy, a.config.BuildID, a.configurationID)
	if err != nil {
		return err
	}
	bootstrapJSON, err := json.Marshal(bootstrap)
	if err != nil {
		return err
	}
	script := []byte(strings.Replace(state.build.BridgeTemplate, offline.BuildDescriptorPlaceholder, string(bootstrapJSON), 1))
	if len(script) > offline.MaxBridgeBytes+offline.MaxCatalogBytes+(16<<10) {
		return offline.ErrQuota
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state.published.Store(&managedOfflineBundle{catalog: catalog, catalogJSON: bytes.Clone(catalogJSON), script: bytes.Clone(script), documents: files})
	return nil
}

// Reserved offline services and frozen runtime inventory bypass page middleware.
// The exact privately bound registration helper is the exception: it must pass
// through normal application headers/middleware before its inert final handler.
// Prepared bytes have a dedicated source URL, never a header-selected variant of
// a normal page URL. Delivery is no-store; explicit policy/native approval plus
// the exact catalog digest governs the worker's separate manual persistence.
func (a *App) serveManagedOffline(w http.ResponseWriter, r *http.Request) bool {
	state := a.managedOffline
	if state == nil || offlineCaptureFor(r) != nil {
		return false
	}
	if helper, _ := r.Context().Value(offlineRegistrationHelperKey{}).(*offlineRegistrationHelperRequest); helper != nil && helper.app == a {
		return false
	}
	local, inside := localRequest(r, a.paths.BasePath)
	if !inside {
		return false
	}
	path := local.URL.EscapedPath()
	service := path == "/_nextgo/offline" || strings.HasPrefix(path, "/_nextgo/offline/")
	decodedService := local.URL.Path == "/_nextgo/offline" || strings.HasPrefix(local.URL.Path, "/_nextgo/offline/")
	if decodedService && !service {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.NotFound(w, r)
		return true
	}
	file, asset := state.files[r.URL.EscapedPath()]
	if !service && !asset {
		return false
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Offline resource requires GET or HEAD", 405)
		return true
	}
	config := state.policy.Config()
	origin, originErr := url.Parse(config.Origin)
	if originErr != nil || r.URL.Host != "" && r.URL.Host != origin.Host || r.URL.Scheme != "" && r.URL.Scheme != origin.Scheme {
		http.Error(w, "Offline resource origin differs", 400)
		return true
	}
	if r.Host != origin.Host || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.User != nil || r.URL.Opaque != "" || r.URL.Fragment != "" || r.Body != nil && r.Body != http.NoBody || r.ContentLength != 0 || len(r.TransferEncoding) != 0 || offlineHeaderPresent(r.Header, "Range") || offlineHeaderPresent(r.Header, "If-Range") {
		http.Error(w, "Offline resource request refused", 400)
		return true
	}
	if asset && !service {
		// Frozen bytes/digests describe the decoded representation. Negotiate
		// transport encoding before a buffered adapter applies its wire limit.
		httpx.Compression(http.HandlerFunc(func(out http.ResponseWriter, request *http.Request) {
			a.writeOfflineFile(out, request, file, "public, max-age=31536000, immutable")
		})).ServeHTTP(w, r)
		return true
	}
	bundle := state.published.Load()
	if bundle == nil {
		http.Error(w, "Offline bundle is not ready", 503)
		return true
	}
	switch path {
	case offline.CatalogPath:
		a.writeOfflineFile(w, r, managedOfflineFile{body: bundle.catalogJSON, contentType: "application/json; charset=utf-8", digest: offlineDigest(bundle.catalogJSON)}, "public, max-age=0, must-revalidate")
		return true
	case offline.WorkerScriptPath:
		scopePath := a.paths.BasePath
		if scopePath == "" {
			scopePath = "/"
		}
		w.Header().Set("Service-Worker-Allowed", scopePath)
		a.writeOfflineFile(w, r, managedOfflineFile{body: bundle.script, contentType: "text/javascript; charset=utf-8", digest: offlineDigest(bundle.script)}, "public, max-age=0, must-revalidate")
		return true
	}
	if !strings.HasPrefix(path, offline.PreparedDocumentsPath) {
		http.NotFound(w, r)
		return true
	}
	if offlineHeaderPresent(r.Header, "Cookie") || offlineHeaderPresent(r.Header, "Authorization") || offlineHeaderPresent(r.Header, "X-Nextgo-Navigate") || offlineHeaderPresent(r.Header, navigation.StreamHeader) || offlineHeaderPresent(r.Header, navigation.PurposeHeader) || offlineHeaderPresent(r.Header, "X-Nextgo-Offline-Prepare") || offlineHeaderPresent(r.Header, offlineGenerationHeader) || len(r.Trailer) != 0 {
		http.Error(w, "Prepared document request refused", 400)
		return true
	}
	rest := strings.TrimPrefix(path, offline.PreparedDocumentsPath)
	generation, digest, split := strings.Cut(rest, "/")
	if !split || !offlineHex(generation, 64) || generation != bundle.catalog.Generation() || !offlineHex(digest, 64) {
		http.NotFound(w, r)
		return true
	}
	file, ok := bundle.documents[digest]
	if !ok {
		http.NotFound(w, r)
		return true
	}
	w.Header().Set(offlineAdmissionHeader, offline.NativeAdmissionV1)
	w.Header().Set(offlineDocumentHeader, file.digest)
	w.Header().Set(offlineGenerationHeader, bundle.catalog.Generation())
	w.Header().Set(navigation.BuildHeader, a.config.BuildID)
	w.Header().Set(navigation.ConfigurationHeader, a.configurationID)
	a.writeOfflineFile(w, r, file, "no-store")
	return true
}
func (a *App) writeOfflineFile(w http.ResponseWriter, r *http.Request, file managedOfflineFile, cacheControl string) {
	w.Header().Set("Content-Type", file.contentType)
	w.Header().Set("Content-Length", fmt.Sprint(len(file.body)))
	w.Header().Set("ETag", `"`+file.digest+`"`)
	w.Header().Set("Cache-Control", cacheControl)
	if r.Method != http.MethodHead {
		_, _ = w.Write(file.body)
	} else {
		w.WriteHeader(http.StatusOK)
	}
}

func offlineHeaderPresent(header http.Header, name string) bool {
	for key := range header {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

// The helper has no page/cache/SSR identity. Bind its original exact request
// before service dispatch so middleware cannot rewrite it into a PageLoader.
type offlineRegistrationHelperKey struct{}
type offlineRegistrationHelperRequest struct {
	app       *App
	method    string
	valid     bool
	workerURL string
}

const offlineRegistrationHelperHTML = "<!doctype html><html data-nextgo-offline-helper=\"1\"><head></head><body></body></html>"

func (a *App) offlineRegistrationHelperRequestValid(r *http.Request) bool {
	if a.managedOffline == nil || r.URL == nil {
		return false
	}
	config := a.managedOffline.policy.Config()
	origin, err := url.Parse(config.Origin)
	if err != nil || r.Host != origin.Host || r.URL.Host != "" && r.URL.Host != origin.Host || r.URL.Scheme != "" && r.URL.Scheme != origin.Scheme {
		return false
	}
	if r.URL.RawPath != "" && r.URL.RawPath != r.URL.Path || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.User != nil || r.URL.Opaque != "" || r.URL.Fragment != "" || r.URL.RawFragment != "" || r.Body != nil && r.Body != http.NoBody || r.ContentLength != 0 || len(r.TransferEncoding) != 0 || len(r.Trailer) != 0 {
		return false
	}
	for _, name := range []string{"Range", "If-Range", "X-Nextgo-Navigate", navigation.StreamHeader, navigation.PurposeHeader, "X-Nextgo-Offline-Prepare", offlineGenerationHeader, offlineAdmissionHeader, offlineDocumentHeader} {
		if offlineHeaderPresent(r.Header, name) {
			return false
		}
	}
	return true
}

func (a *App) bindOfflineRegistrationHelper(r *http.Request, w http.ResponseWriter) (*http.Request, http.ResponseWriter) {
	if a.managedOffline == nil || offlineCaptureFor(r) != nil {
		return r, w
	}
	local, inside := localRequest(r, a.paths.BasePath)
	if !inside || local.URL.EscapedPath() != offline.RegistrationHelperPath {
		return r, w
	}
	binding := &offlineRegistrationHelperRequest{app: a, method: r.Method, valid: a.offlineRegistrationHelperRequestValid(r)}
	if descriptor, err := a.offlineClientDescriptor(); err == nil {
		if _, err = offline.RegistrationHelperURL(descriptor.WorkerURL); err == nil {
			binding.workerURL = descriptor.WorkerURL
		}
	}
	r = r.WithContext(context.WithValue(r.Context(), offlineRegistrationHelperKey{}, binding))
	return r, wrapOfflineRegistrationHelperWriter(w, binding.workerURL)
}

func (a *App) serveOfflineRegistrationHelper(w http.ResponseWriter, r *http.Request) bool {
	binding, _ := r.Context().Value(offlineRegistrationHelperKey{}).(*offlineRegistrationHelperRequest)
	if binding == nil {
		// A middleware rewrite may not acquire the helper through a different URL.
		if a.managedOffline == nil || r.URL.EscapedPath() != offline.RegistrationHelperPath {
			return false
		}
		w.Header().Set("Cache-Control", "no-store, no-transform")
		http.NotFound(w, r)
		return true
	}
	if binding.app != a || !binding.valid || binding.method != r.Method || r.URL.EscapedPath() != offline.RegistrationHelperPath || !a.offlineRegistrationHelperRequestValid(r) {
		http.Error(w, "Offline helper request refused", http.StatusBadRequest)
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Offline helper requires GET or HEAD", http.StatusMethodNotAllowed)
		return true
	}
	if binding.workerURL == "" {
		http.Error(w, "Offline helper unavailable", http.StatusServiceUnavailable)
		return true
	}
	if a.managedOffline.published.Load() == nil {
		http.Error(w, "Offline bundle is not ready", http.StatusServiceUnavailable)
		return true
	}
	// This handler originates no cookie. Trusted middleware's session refresh
	// must survive; discarding it could rotate server state without the client.
	for _, name := range []string{"ETag", "Last-Modified", "Content-Encoding", "Content-Range", "Refresh", "Location", "Content-Disposition"} {
		w.Header().Del(name)
	}
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", fmt.Sprint(len(offlineRegistrationHelperHTML)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, offlineRegistrationHelperHTML)
	}
	return true
}

// HeaderRules can install a final private-response writer which rewrites the
// cache policy. This helper-only outer writer preserves no-transform even then,
// and appends (rather than replaces) the restrictive document CSP at final write.
// It cannot attest socket closure, a worker client disappearing, or hostile
// middleware behavior. All ordinary route writer behavior remains untouched.
type offlineRegistrationHelperWriter struct {
	http.ResponseWriter
	workerURL string
	wrote     bool
}

func (w *offlineRegistrationHelperWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *offlineRegistrationHelperWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	if status >= 200 || status == http.StatusSwitchingProtocols {
		w.wrote = true
		h := w.Header()
		h.Set("Cache-Control", "no-store, no-transform")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		worker := "'none'"
		if w.workerURL != "" {
			worker = w.workerURL
		}
		h.Add("Content-Security-Policy", "default-src 'none'; script-src 'none'; worker-src "+worker+"; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-src 'none'; object-src 'none'; frame-ancestors 'self'")
		if h.Get("X-Frame-Options") == "" {
			h.Set("X-Frame-Options", "SAMEORIGIN")
		}
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *offlineRegistrationHelperWriter) Write(body []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

type offlineRegistrationHelperFlushWriter struct {
	*offlineRegistrationHelperWriter
}

func (w offlineRegistrationHelperFlushWriter) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	w.ResponseWriter.(http.Flusher).Flush()
}

type offlineRegistrationHelperHijackWriter struct {
	*offlineRegistrationHelperWriter
}

func (w offlineRegistrationHelperHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.(http.Hijacker).Hijack()
}

type offlineRegistrationHelperFlushHijackWriter struct {
	*offlineRegistrationHelperWriter
}

func (w offlineRegistrationHelperFlushHijackWriter) Flush() {
	offlineRegistrationHelperFlushWriter{w.offlineRegistrationHelperWriter}.Flush()
}
func (w offlineRegistrationHelperFlushHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return offlineRegistrationHelperHijackWriter{w.offlineRegistrationHelperWriter}.Hijack()
}
func wrapOfflineRegistrationHelperWriter(w http.ResponseWriter, workerURL string) http.ResponseWriter {
	wrapped := &offlineRegistrationHelperWriter{ResponseWriter: w, workerURL: workerURL}
	_, flush := w.(http.Flusher)
	_, hijack := w.(http.Hijacker)
	if flush && hijack {
		return offlineRegistrationHelperFlushHijackWriter{wrapped}
	}
	if flush {
		return offlineRegistrationHelperFlushWriter{wrapped}
	}
	if hijack {
		return offlineRegistrationHelperHijackWriter{wrapped}
	}
	return wrapped
}

// Bound helper requests use no content transformation, including a middleware
// denial. Normal requests keep the existing compression/capture dispatcher.
func (a *App) offlineRegistrationHelperHandler(plain, normal http.Handler) http.Handler {
	if a.managedOffline == nil {
		return normal
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if binding, _ := r.Context().Value(offlineRegistrationHelperKey{}).(*offlineRegistrationHelperRequest); binding != nil && binding.app == a {
			plain.ServeHTTP(w, r)
			return
		}
		normal.ServeHTTP(w, r)
	})
}
