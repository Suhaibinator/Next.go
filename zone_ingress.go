package nextgo

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/zonecontract"
)

// ZoneIngressOptions bounds one immutable release ingress. Transport is cloned
// and owned; environment proxies, connection reuse and HTTP/2 are disabled so a
// native transport cannot replay a committed unsafe request. Dial/TLS callbacks
// remain trusted native code and must cooperate with cancellation.
type ZoneIngressOptions struct {
	Transport                         *http.Transport
	Timeout                           time.Duration
	MaxRequestBytes, MaxResponseBytes int64
	MaxInFlight                       int
	// TrustedTLSOffload explicitly admits HTTP from a trusted TLS terminator for
	// a declared HTTPS origin. Forwarded headers never establish this policy.
	TrustedTLSOffload bool
}

type zoneIngressRelease struct {
	spec   zonecontract.ReleaseSpec
	origin *url.URL
}
type zoneIngressMount struct {
	binding  zonecontract.Binding
	active   *zoneIngressRelease
	releases map[string]*zoneIngressRelease
}
type zoneIngressRoute struct {
	mount   *zoneIngressMount
	release *zoneIngressRelease
	asset   bool
}

// ZoneIngress serves one frozen manifest of independently built endpoints.
// The manifest/expectation headers identify compatibility, not caller or tenant
// authorization. No response cache, upstream failover or redirect following is
// installed. Updating a release requires constructing a new ingress.
type ZoneIngress struct {
	options   ZoneIngressOptions
	hosts     map[string][]*zoneIngressMount
	transport *http.Transport
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	closed    bool
	active    int
	idle      chan struct{}
}

func NewZoneIngress(ctx context.Context, manifest zonecontract.IngressManifest, options ZoneIngressOptions) (*ZoneIngress, error) {
	if ctx == nil {
		return nil, errors.New("nextgo: zone ingress requires a startup context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manifest, err := zonecontract.NormalizeIngressManifest(manifest)
	if err != nil {
		return nil, err
	}
	options, err = normalizeZoneIngressOptions(options)
	if err != nil {
		return nil, err
	}
	transport := zoneIngressTransport(options)
	owned, cancel := context.WithCancel(context.Background())
	idle := make(chan struct{})
	close(idle)
	ingress := &ZoneIngress{options: options, hosts: map[string][]*zoneIngressMount{}, transport: transport, ctx: owned, cancel: cancel, idle: idle}
	// The clone, not a caller's mutable transport/configuration, is retained.
	ingress.options.Transport = nil
	for _, zone := range manifest.Zones {
		mount := &zoneIngressMount{binding: zone.Binding.Clone(), releases: map[string]*zoneIngressRelease{}}
		for _, release := range zone.Releases {
			origin, _ := url.Parse(release.Origin)
			entry := &zoneIngressRelease{spec: release.Artifact.Release.Clone(), origin: origin}
			mount.releases[entry.spec.ReleaseID] = entry
			if entry.spec.ReleaseID == zone.ActiveRelease {
				mount.active = entry
			}
		}
		ingress.hosts[mount.binding.Host] = append(ingress.hosts[mount.binding.Host], mount)
	}
	for _, mounts := range ingress.hosts {
		slices.SortFunc(mounts, func(a, b *zoneIngressMount) int { return len(b.binding.BasePath) - len(a.binding.BasePath) })
	}
	if err := ctx.Err(); err != nil {
		cancel()
		transport.CloseIdleConnections()
		return nil, err
	}
	return ingress, nil
}

func normalizeZoneIngressOptions(options ZoneIngressOptions) (ZoneIngressOptions, error) {
	if options.Timeout == 0 {
		options.Timeout = 30 * time.Second
	}
	if options.MaxRequestBytes == 0 {
		options.MaxRequestBytes = 2 << 20
	}
	if options.MaxResponseBytes == 0 {
		options.MaxResponseBytes = 8 << 20
	}
	if options.MaxInFlight == 0 {
		options.MaxInFlight = 16
	}
	if options.Timeout < time.Millisecond || options.Timeout > 5*time.Minute || options.MaxRequestBytes < 1 || options.MaxRequestBytes > 64<<20 || options.MaxResponseBytes < 1 || options.MaxResponseBytes > 64<<20 || options.MaxInFlight < 1 || options.MaxInFlight > 1024 {
		return options, errors.New("nextgo: invalid zone ingress budgets")
	}
	return options, nil
}

func (ingress *ZoneIngress) selectRoute(request *http.Request) (zoneIngressRoute, int) {
	var empty zoneIngressRoute
	if request == nil || request.URL == nil {
		return empty, http.StatusBadRequest
	}
	host, err := paths.CanonicalHost(request.Host)
	if err != nil {
		return empty, http.StatusBadRequest
	}
	mounts := ingress.hosts[host]
	if len(mounts) == 0 {
		return empty, http.StatusMisdirectedRequest
	}
	if request.URL.User != nil || request.URL.Opaque != "" || request.URL.Fragment != "" || request.URL.Scheme != "" && request.URL.Scheme != "http" && request.URL.Scheme != "https" {
		return empty, http.StatusBadRequest
	}
	if request.URL.Host != "" {
		authority, err := paths.CanonicalHost(request.URL.Host)
		if err != nil || authority != host {
			return empty, http.StatusBadRequest
		}
	}
	escaped := request.URL.EscapedPath()
	if len(escaped) > 8192 {
		return empty, http.StatusRequestURITooLong
	}
	if _, err := (paths.Config{}).URL(escaped); err != nil {
		return empty, http.StatusBadRequest
	}
	for _, mount := range mounts {
		local, inside := paths.Strip(mount.binding.BasePath, escaped)
		if !inside {
			continue
		}
		public, _ := url.Parse(mount.active.spec.PublicOrigin)
		if public.Scheme == "https" && request.TLS == nil && !ingress.options.TrustedTLSOffload || public.Scheme == "http" && request.TLS != nil {
			return empty, http.StatusBadRequest
		}
		if local == "/_nextgo/releases" || strings.HasPrefix(local, "/_nextgo/releases/") {
			if request.Method != http.MethodGet && request.Method != http.MethodHead {
				return empty, http.StatusNotFound
			}
			rest := strings.TrimPrefix(local, "/_nextgo/releases/")
			releaseID, suffix, found := strings.Cut(rest, "/")
			release := mount.releases[releaseID]
			if !found || release == nil || !zoneIngressAssetSuffix("/"+suffix, release.spec.RuntimeID) {
				return empty, http.StatusNotFound
			}
			return zoneIngressRoute{mount: mount, release: release, asset: true}, 0
		}
		if zoneUnversionedReservedPath(local) {
			return empty, http.StatusNotFound
		}
		return zoneIngressRoute{mount: mount, release: mount.active}, 0
	}
	return empty, http.StatusNotFound
}

func zoneIngressAssetSuffix(path, runtimeID string) bool {
	if strings.HasSuffix(path, "/") {
		return false
	}
	return strings.HasPrefix(path, "/assets/") && len(path) > len("/assets/") || strings.HasPrefix(path, "/_nextgo/"+runtimeID+"/") && len(path) > len("/_nextgo/"+runtimeID+"/")
}

func (ingress *ZoneIngress) admit() bool {
	ingress.mu.Lock()
	defer ingress.mu.Unlock()
	if ingress.closed || ingress.active >= ingress.options.MaxInFlight {
		return false
	}
	if ingress.active == 0 {
		ingress.idle = make(chan struct{})
	}
	ingress.active++
	return true
}
func (ingress *ZoneIngress) release() {
	ingress.mu.Lock()
	defer ingress.mu.Unlock()
	ingress.active--
	if ingress.active == 0 {
		close(ingress.idle)
	}
}

func (ingress *ZoneIngress) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if ingress == nil {
		zoneIngressError(w, http.StatusServiceUnavailable)
		return
	}
	route, status := ingress.selectRoute(request)
	if status != 0 {
		zoneIngressError(w, status)
		return
	}
	if request.Method == http.MethodConnect || request.Header.Get("Upgrade") != "" || zoneIngressConnectionToken(request.Header, "upgrade") {
		zoneIngressError(w, http.StatusNotImplemented)
		return
	}
	if zoneIngressForbiddenConnection(request.Header) {
		zoneIngressError(w, http.StatusBadRequest)
		return
	}
	if !zoneIngressHeadersBounded(request.Header) {
		zoneIngressError(w, http.StatusRequestHeaderFieldsTooLarge)
		return
	}
	if !ingress.admit() {
		w.Header().Set("Retry-After", "1")
		zoneIngressError(w, http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), ingress.options.Timeout)
	stopShutdown := context.AfterFunc(ingress.ctx, cancel)
	defer stopShutdown()
	defer cancel()
	downstreamDone := make(chan struct{})
	defer close(downstreamDone)
	controller := http.NewResponseController(w)
	deadline, _ := ctx.Deadline()
	_ = controller.SetReadDeadline(deadline)
	// Bound real socket writes as well as reads. A custom writer that ignores
	// deadlines remains charged until its physical Write/Flush actually returns.
	_ = controller.SetWriteDeadline(deadline)
	ready, chunks := make(chan zoneIngressHead, 1), make(chan zoneIngressChunk, 1)
	go ingress.dispatch(ctx, request, route, ready, chunks, downstreamDone)
	var head zoneIngressHead
	select {
	case head = <-ready:
	case <-ctx.Done():
		_ = controller.SetReadDeadline(time.Now())
		w.Header().Set("Connection", "close")
		zoneIngressError(w, http.StatusGatewayTimeout)
		return
	}
	if head.err != nil {
		_ = controller.SetReadDeadline(time.Now())
		w.Header().Set("Connection", "close")
		zoneIngressError(w, head.status)
		return
	}
	_ = controller.SetReadDeadline(time.Time{})
	for key, values := range head.headers {
		w.Header()[key] = slices.Clone(values)
	}
	zonecontract.StripExpectation(w.Header())
	// Keep completion owned by this handler, so a late upstream read error or
	// overflow cannot look like a clean Content-Length-delimited HTTP success.
	if head.body {
		w.Header().Del("Content-Length")
	}
	for _, name := range head.trailers {
		w.Header().Add("Trailer", name)
	}
	w.WriteHeader(head.status)
	if head.body {
		if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			panic(http.ErrAbortHandler)
		}
	}
	for {
		select {
		case chunk := <-chunks:
			if len(chunk.data) != 0 {
				if _, err := w.Write(chunk.data); err != nil {
					panic(http.ErrAbortHandler)
				}
				if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
					panic(http.ErrAbortHandler)
				}
			}
			if chunk.err != nil {
				panic(http.ErrAbortHandler)
			}
			if chunk.done {
				for name, values := range chunk.trailers {
					w.Header()[http.TrailerPrefix+name] = slices.Clone(values)
				}
				// Keep the deadline for net/http's final framing after return.
				// The native server resets it before the next request.
				return
			}
		case <-ctx.Done():
			panic(http.ErrAbortHandler)
		}
	}
}

func zoneIngressError(w http.ResponseWriter, status int) {
	zonecontract.StripExpectation(w.Header())
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.Error(w, "Zone request could not be completed", status)
}

// Close stops admission, cancels admitted work and joins physical workers. A
// canceled context may return while an uncooperative native Read/Close/Dial is
// still running; that worker retains its admission until it actually finishes.
// Startup ctx is not the lifetime context of a successfully constructed ingress.
func (ingress *ZoneIngress) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("nextgo: zone ingress close requires a context")
	}
	if ingress == nil {
		return nil
	}
	ingress.mu.Lock()
	ingress.closed = true
	idle := ingress.idle
	ingress.mu.Unlock()
	if ingress.cancel != nil {
		ingress.cancel()
	}
	if ingress.transport != nil {
		ingress.transport.CloseIdleConnections()
	}
	if idle == nil {
		return nil
	}
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
