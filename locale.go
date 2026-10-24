package nextgo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/paths"
)

// LocaleConfig adds managed subpath/domain routing. Codes are a bounded ASCII
// language-tag subset, not a translation/formatting library. At most 32 codes,
// 16 domains and 64 total mounts are accepted. Config is frozen by New.
// PrefixDefault prefixes the default too. Otherwise the default is unprefixed
// and explicit default-prefix aliases remain available. Detect negotiates bare
// page URLs (all with PrefixDefault, otherwise only home) using cookie/header.
// Internal services, endpoints and shared assets bypass language negotiation.
type LocaleConfig struct {
	Codes         []string
	Default       string
	PrefixDefault bool
	Detect        bool
	Cookie        string
	Directions    map[string]string
	Domains       []LocaleDomain
}

// LocaleDomain is an exact configured host[:port]. HTTPS is used for generated
// domain links unless HTTP is explicitly set for local development. Host
// and forwarded headers never supply an unconfigured redirect authority.
type LocaleDomain struct {
	Host, Default string
	HTTP          bool
}

type localeContextKey struct{}
type localeStaticBudgetKey struct{}
type localeStaticBudget struct{ rows, generated, bytes atomic.Int64 }
type localeRouter struct {
	codes                   []string
	lookup                  map[string]string
	directions              map[string]string
	defaultCode, cookie, id string
	prefixDefault, detect   bool
	domains                 []LocaleDomain
	root                    paths.Config
	mounts                  []*localeScope
}
type localeScope struct {
	router                                     *localeRouter
	code, defaultCode, host, direction, prefix string
	http, alias                                bool
	app                                        *App
}

var ErrLocaleScope = errors.New("nextgo: locale helpers require a managed locale scope")

// CurrentLocale is available in loaders, layouts, metadata, APIs/actions,
// completion work and static enumeration. It is empty outside managed routing.
func CurrentLocale(ctx context.Context) string {
	if s := localeFrom(ctx); s != nil {
		return s.code
	}
	return ""
}

// AvailableLocales returns an owned list in configured negotiation order.
func AvailableLocales(ctx context.Context) []string {
	if s := localeFrom(ctx); s != nil {
		return slices.Clone(s.router.codes)
	}
	return nil
}
func localeFrom(ctx context.Context) *localeScope {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(localeContextKey{}).(*localeScope)
	return s
}

// LocaleURL resolves a logical app URL in a requested locale, preserving query
// and fragment. A configured default domain for that language is preferred;
// domain results use only configured scheme/authority, including the current
// domain. Subpath-only results are relative. Cross-locale navigation loads a
// complete document.
func LocaleURL(ctx context.Context, code, logical string) (string, error) {
	s := localeFrom(ctx)
	if s == nil {
		return "", ErrLocaleScope
	}
	return s.router.localeURL(s, code, logical, false)
}
func MustLocaleURL(ctx context.Context, code, logical string) string {
	u, err := LocaleURL(ctx, code, logical)
	if err != nil {
		panic(err)
	}
	return u
}

// LocaleCookie builds an explicit preference cookie for a handler/action to
// publish. secure must reflect the deployment's public HTTPS policy; forwarded
// headers are not inferred. The cookie is host-only, HttpOnly, SameSite=Lax and
// scoped to the global app mount. Publishing it must use a private response.
func LocaleCookie(ctx context.Context, code string, secure bool) (*http.Cookie, error) {
	s := localeFrom(ctx)
	if s == nil {
		return nil, ErrLocaleScope
	}
	canonical, ok := s.router.lookup[strings.ToLower(code)]
	if !ok {
		return nil, fmt.Errorf("nextgo: unknown locale %q", code)
	}
	path := s.router.root.BasePath
	if path == "" {
		path = "/"
	}
	if strings.HasPrefix(s.router.cookie, "__Host-") && (!secure || path != "/") || strings.HasPrefix(s.router.cookie, "__Secure-") && !secure {
		return nil, fmt.Errorf("nextgo: locale cookie prefix requires a secure root policy")
	}
	return &http.Cookie{Name: s.router.cookie, Value: canonical, Path: path, Secure: secure, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 31536000}, nil
}

func validLocaleCode(s string) bool {
	if len(s) < 2 || len(s) > 63 {
		return false
	}
	parts := strings.Split(s, "-")
	for i, p := range parts {
		if len(p) == 0 || len(p) > 8 || i == 0 && len(p) < 2 {
			return false
		}
		for _, r := range p {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
				return false
			}
		}
	}
	return true
}
func localeHost(raw string) (string, error) { return paths.CanonicalHost(raw) }

// Browsers omit the public scheme's default port. Normalize that alias using
// configured policy, never a forwarded header or an inferred proxy scheme.
func localeDomainHost(raw string, plainHTTP bool) (string, error) {
	host, err := localeHost(raw)
	if err != nil {
		return "", err
	}
	u := &url.URL{Host: host}
	if plainHTTP && u.Port() == "80" || !plainHTTP && u.Port() == "443" {
		host = u.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	}
	return host, nil
}

func localeDomainAuthorities(d LocaleDomain) []string {
	u := &url.URL{Host: d.Host}
	if u.Port() != "" {
		return []string{d.Host}
	}
	port := "443"
	if d.HTTP {
		port = "80"
	}
	return []string{d.Host, net.JoinHostPort(u.Hostname(), port)}
}

func newLocaleApp(c Config) (*App, error) {
	input := c.Locales
	r := &localeRouter{codes: slices.Clone(input.Codes), lookup: map[string]string{}, directions: map[string]string{}, prefixDefault: input.PrefixDefault, detect: input.Detect, cookie: input.Cookie, root: paths.Config{BasePath: c.BasePath, AssetPrefix: c.AssetPrefix, TrailingSlash: c.TrailingSlash}}
	if err := r.root.Validate(); err != nil {
		return nil, err
	}
	if len(r.codes) == 0 || len(r.codes) > 32 || len(input.Domains) > 16 {
		return nil, fmt.Errorf("nextgo: locale routing requires 1..32 codes and at most 16 domains")
	}
	for _, code := range r.codes {
		key := strings.ToLower(code)
		if !validLocaleCode(code) || r.lookup[key] != "" {
			return nil, fmt.Errorf("nextgo: invalid or duplicate locale %q", code)
		}
		if key == "assets" || key == "api" {
			return nil, fmt.Errorf("nextgo: reserved locale %q", code)
		}
		r.lookup[key] = code
		r.directions[code] = "ltr"
	}
	r.defaultCode = r.lookup[strings.ToLower(input.Default)]
	if r.defaultCode == "" {
		return nil, fmt.Errorf("nextgo: default locale must be configured")
	}
	directionKeys := map[string]bool{}
	for key, dir := range input.Directions {
		code := r.lookup[strings.ToLower(key)]
		if code == "" || directionKeys[code] || dir != "ltr" && dir != "rtl" {
			return nil, fmt.Errorf("nextgo: invalid locale direction")
		}
		directionKeys[code] = true
		r.directions[code] = dir
	}
	if r.cookie == "" {
		r.cookie = "nextgo_locale"
	}
	if err := (&http.Cookie{Name: r.cookie, Value: "en"}).Valid(); err != nil || len(r.cookie) > 64 {
		return nil, fmt.Errorf("nextgo: invalid locale cookie name")
	}
	seen := map[string]bool{}
	for _, d := range input.Domains {
		host, err := localeDomainHost(d.Host, d.HTTP)
		if err != nil {
			return nil, fmt.Errorf("nextgo: invalid or duplicate locale domain %q", d.Host)
		}
		d.Host = host
		for _, authority := range localeDomainAuthorities(d) {
			if seen[authority] {
				return nil, fmt.Errorf("nextgo: overlapping locale domain authority %q", authority)
			}
			seen[authority] = true
		}
		code := r.lookup[strings.ToLower(d.Default)]
		if code == "" {
			return nil, fmt.Errorf("nextgo: invalid locale domain default")
		}
		r.domains = append(r.domains, LocaleDomain{Host: host, Default: code, HTTP: d.HTTP})
	}
	domains := r.domains
	if len(domains) == 0 {
		domains = []LocaleDomain{{Default: r.defaultCode}}
	}
	count := len(domains) * len(r.codes)
	if !r.prefixDefault {
		count += len(domains)
	}
	if count > 64 {
		return nil, fmt.Errorf("nextgo: locale routing exceeds 64 mounts")
	}
	// Reserve language prefixes rather than silently shadowing application routes.
	for _, route := range c.Routes {
		part := strings.Split(strings.TrimPrefix(route.Pattern, "/"), "/")[0]
		if r.lookup[strings.ToLower(part)] != "" {
			return nil, fmt.Errorf("route %s conflicts with a locale prefix", route.Pattern)
		}
	}
	encoded, _ := json.Marshal(struct {
		Codes           []string
		Default, Cookie string
		Directions      map[string]string
		Domains         []LocaleDomain
		Prefix, Detect  bool
	}{r.codes, r.defaultCode, r.cookie, r.directions, r.domains, r.prefixDefault, r.detect})
	sum := sha256.Sum256(encoded)
	r.id = hex.EncodeToString(sum[:])
	if c.Cache == nil {
		c.Cache = cache.New(128, cache.WithMaxBytes(64<<20, nil))
	}
	assets := c.AssetPrefix
	if assets == "" {
		assets = c.BasePath
		if assets == "" {
			assets = "/"
		}
	}
	c.Locales = nil
	var defaultApp *App
	for _, domain := range domains {
		for _, code := range r.codes {
			prefixes := []string{r.prefix(code, domain.Default, false)}
			if code == domain.Default && !r.prefixDefault {
				prefixes = append(prefixes, "/"+code)
			}
			for i, prefix := range prefixes {
				scope := &localeScope{router: r, code: code, defaultCode: domain.Default, host: domain.Host, http: domain.HTTP, direction: r.directions[code], prefix: prefix, alias: i > 0}
				child := c
				child.prerenderDeferred = true
				child.BasePath = c.BasePath + prefix
				child.AssetPrefix = assets
				child.localeScope = scope
				app, err := New(child)
				if err != nil {
					for _, s := range r.mounts {
						_ = s.app.after.Close(context.Background())
					}
					return nil, err
				}
				if defaultApp != nil {
					_ = app.after.Close(context.Background())
					app.after = defaultApp.after
					app.proxy.closeIdle()
					app.proxy = defaultApp.proxy
				}
				scope.app = app
				r.mounts = append(r.mounts, scope)
				if defaultApp == nil {
					defaultApp = app
				}
			}
		}
	}
	wrapper := *defaultApp // App contains immutable configuration and pointer-owned workers.
	wrapper.paths = r.root
	wrapper.locales = r
	wrapper.config.prerenderDeferred = false
	if err := wrapper.initPrerenderRuntime(); err != nil {
		_ = wrapper.after.Close(context.Background())
		return nil, err
	}
	return &wrapper, nil
}
func (r *localeRouter) prefix(code, defaultCode string, force bool) string {
	if !force && !r.prefixDefault && code == defaultCode {
		return ""
	}
	return "/" + code
}
func (s *localeScope) identity() string {
	sum := sha256.Sum256([]byte(s.router.id + "\x00" + s.code + "\x00" + s.host + "\x00" + s.prefix))
	return hex.EncodeToString(sum[:])
}
func (a *App) localeMetadata(p Page) Page {
	if s := a.config.localeScope; s != nil {
		if p.Metadata.Language == "" {
			p.Metadata.Language = s.code
		}
		if p.Metadata.Direction == "" {
			p.Metadata.Direction = s.direction
		}
	}
	return p
}

func (r *localeRouter) scope(host, code string, explicit bool) *localeScope {
	for _, s := range r.mounts {
		if s.host == host && s.code == code && s.alias == (!r.prefixDefault && explicit && code == s.defaultCode) {
			return s
		}
	}
	return nil
}
func (r *localeRouter) localeURL(current *localeScope, code, logical string, force bool) (string, error) {
	code = r.lookup[strings.ToLower(code)]
	if code == "" {
		return "", fmt.Errorf("nextgo: unknown locale")
	}
	host, def, httpMode := current.host, current.defaultCode, current.http
	if len(r.domains) > 0 && code != current.defaultCode {
		for _, d := range r.domains {
			if d.Default == code {
				host, def, httpMode = d.Host, d.Default, d.HTTP
				break
			}
		}
	}
	p := r.root
	p.BasePath += r.prefix(code, def, force)
	result, err := p.URL(logical)
	if err != nil {
		return "", err
	}
	if host != "" {
		scheme := "https"
		if httpMode {
			scheme = "http"
		}
		result = scheme + "://" + host + result
	}
	return result, nil
}

func (r *localeRouter) serveHTTP(w http.ResponseWriter, req *http.Request) {
	// Shared assets stay outside negotiation and can be served from a CDN host.
	if local, ok := localRequest(req, r.root.AssetPath()); ok && assetRequest(local) {
		r.mounts[0].app.AssetHandler().ServeHTTP(w, local)
		return
	}
	host := ""
	def := r.defaultCode
	if len(r.domains) > 0 {
		normalized, err := localeHost(req.Host)
		if err != nil {
			http.Error(w, "Invalid locale host", 400)
			return
		}
		found := false
		for _, d := range r.domains {
			candidate, _ := localeDomainHost(normalized, d.HTTP)
			if d.Host == candidate {
				host, def, found = d.Host, d.Default, true
				break
			}
		}
		if !found {
			http.Error(w, "Unconfigured locale domain", http.StatusMisdirectedRequest)
			return
		}
	}
	local, ok := r.root.Strip(req.URL.EscapedPath())
	if !ok {
		http.NotFound(w, req)
		return
	}
	first, _, _ := strings.Cut(strings.TrimPrefix(local, "/"), "/")
	decoded, err := url.PathUnescape(first)
	if err != nil {
		http.Error(w, "Invalid locale path", 400)
		return
	}
	code := r.lookup[strings.ToLower(decoded)]
	if code != "" {
		scope := r.scope(host, code, true)
		if first != code {
			rest := strings.TrimPrefix(local, "/"+first)
			target := r.root.BasePath + "/" + code + rest
			if req.URL.RawQuery != "" || req.URL.ForceQuery {
				target += "?" + req.URL.RawQuery
			}
			var headersOK bool
			w, headersOK = scope.app.localeRedirectHeaders(w, req, rest)
			if !headersOK {
				return
			}
			localeRedirect(w, req, target, 308)
			return
		}
		scope.app.ServeHTTP(w, req)
		return
	}
	scope := r.scope(host, def, false)
	// Preserve existing unprefixed endpoint URLs, including POST bodies/methods.
	if strings.HasPrefix(local, "/_nextgo/") || scope.endpoint(local) {
		clone := req.Clone(req.Context())
		u := *req.URL
		clone.URL = &u
		escaped := r.root.BasePath + scope.prefix + local
		u.Path, _ = url.PathUnescape(escaped)
		u.RawPath = escaped
		scope.app.ServeHTTP(w, clone)
		return
	}
	selected := def
	if r.detect && (r.prefixDefault || local == "/") && (req.Method == "GET" || req.Method == "HEAD") {
		w.Header().Add("Vary", "Accept-Language")
		w.Header().Add("Vary", "Cookie")
		if cookie, err := req.Cookie(r.cookie); err == nil {
			selected = r.lookup[strings.ToLower(cookie.Value)]
		}
		if selected == "" || selected == def {
			if cookie, err := req.Cookie(r.cookie); err != nil || r.lookup[strings.ToLower(cookie.Value)] == "" {
				selected = r.negotiate(req.Header.Get("Accept-Language"), def)
			}
		}
	}
	if r.prefixDefault || selected != def {
		logical := local
		if req.URL.RawQuery != "" || req.URL.ForceQuery {
			logical += "?" + req.URL.RawQuery
		}
		// An explicit destination prefix prevents conflicting cross-domain cookies
		// from bouncing a negotiated redirect back to its source domain.
		target, err := r.localeURL(scope, selected, logical, len(r.domains) > 0)
		if err != nil {
			http.Error(w, "Invalid locale URL", 400)
			return
		}
		status := http.StatusTemporaryRedirect
		if !r.detect {
			status = http.StatusPermanentRedirect
		}
		var headersOK bool
		w, headersOK = scope.app.localeRedirectHeaders(w, req, local)
		if !headersOK {
			return
		}
		localeRedirect(w, req, target, status)
		return
	}
	scope.app.ServeHTTP(w, req)
}
func (s *localeScope) endpoint(escaped string) bool {
	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return false
	}
	for path := range s.app.config.Handlers {
		if decoded == path || strings.HasSuffix(path, "/") && strings.HasPrefix(decoded, path) {
			return true
		}
	}
	for _, route := range s.app.routes {
		if _, ok := match(route.Pattern, escaped); ok {
			return route.API != nil
		}
	}
	return false
}
func localeRedirect(w http.ResponseWriter, r *http.Request, target string, status int) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.Redirect(w, r, target, status)
}
func (r *localeRouter) negotiate(header, fallback string) string {
	if len(header) > 8192 {
		return fallback
	}
	type preference struct {
		tag string
		q   float64
	}
	var prefs []preference
	for _, part := range strings.Split(header, ",") {
		if len(prefs) >= 64 {
			break
		}
		fields := strings.Split(strings.TrimSpace(part), ";")
		tag := strings.ToLower(strings.TrimSpace(fields[0]))
		q := 1.0
		valid := tag == "*" || validLocaleCode(tag)
		if len(fields) > 2 {
			valid = false
		}
		if len(fields) == 2 {
			key, value, ok := strings.Cut(strings.TrimSpace(fields[1]), "=")
			var err error
			q, err = localeQuality(value)
			valid = valid && ok && strings.EqualFold(strings.TrimSpace(key), "q") && err == nil
		}
		if valid && q > 0 {
			prefs = append(prefs, preference{tag, q})
		}
	}
	slices.SortStableFunc(prefs, func(a, b preference) int {
		if a.q > b.q {
			return -1
		}
		if a.q < b.q {
			return 1
		}
		return 0
	})
	for _, p := range prefs {
		if p.tag == "*" {
			return fallback
		}
		tag := p.tag
		for tag != "" {
			if code := r.lookup[tag]; code != "" {
				return code
			}
			at := strings.LastIndex(tag, "-")
			if at < 0 {
				break
			}
			tag = tag[:at]
		}
		for _, code := range r.codes {
			if strings.Split(strings.ToLower(code), "-")[0] == tag {
				return code
			}
		}
	}
	return fallback
}
func (r *localeRouter) staticPaths(ctx context.Context, domain string) ([]string, error) {
	domain, err := r.exportDomain(domain)
	if err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, localeStaticBudgetKey{}, &localeStaticBudget{})
	result := []string{}
	total := 0
	for _, s := range r.mounts {
		if s.alias || s.host != domain {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		values, err := s.app.StaticPaths(ctx)
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			p := r.root.SlashPath(s.prefix + value)
			if p == "" {
				p = "/"
			}
			total += len(p)
			if len(result) >= 10000 || len(p) > 8192 || total > 16<<20 {
				return nil, fmt.Errorf("nextgo: locale static enumeration exceeds bounds")
			}
			result = append(result, p)
		}
	}
	sort.Strings(result)
	return result, nil
}

func localeQuality(raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "0" || raw == "1" {
		return strconv.ParseFloat(raw, 64)
	}
	if len(raw) < 2 || len(raw) > 5 || raw[1] != '.' || raw[0] != '0' && raw[0] != '1' {
		return 0, fmt.Errorf("invalid language quality")
	}
	for _, r := range raw[2:] {
		if r < '0' || r > '9' || raw[0] == '1' && r != '0' {
			return 0, fmt.Errorf("invalid language quality")
		}
	}
	return strconv.ParseFloat(raw, 64)
}
