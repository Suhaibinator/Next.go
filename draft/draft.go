// Package draft provides signed, expiring server-only preview sessions.
// A valid preview bypasses framework Store caches, never application auth.
package draft

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/paths"
	_ "github.com/Suhaibinator/Next.go/serveronly"
)

// Options is frozen by New. Keys contain 32–64 random secret bytes per key;
// keep them outside source/browser props. ActiveKey issues new sessions while
// up to four configured keys verify older sessions during bounded rotation.
type Options struct {
	Keys      map[string][]byte
	ActiveKey string
	// Scope is a stable application/preview epoch, not a user-controlled value.
	Scope            string
	CookieName, Path string
	// TTL defaults to one hour; accepted values are whole seconds, 1m–24h.
	TTL time.Duration
	// AllowInsecure is for explicit local HTTP deployments. HTTPS is the default.
	AllowInsecure bool
}

type Manager struct {
	keys                      map[string][]byte
	active, scope, name, path string
	ttl                       time.Duration
	secure                    bool
	now                       func() time.Time
}

type enabledKey struct{}

// Validate rejects a zero-value manager. Use New before attaching it to App.
func (m *Manager) Validate() error {
	if m == nil || m.now == nil || m.keys[m.active] == nil {
		return errors.New("draft: construct the manager with New")
	}
	return nil
}

// Enabled reports the verified state installed by Config.Draft or BindRequest.
// Missing, expired, ambiguous and invalid cookies all produce false.
func Enabled(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	enabled, _ := ctx.Value(enabledKey{}).(bool)
	return enabled
}

// Configured distinguishes a verified disabled session from an application
// without managed preview support. It never exposes the signing manager.
func Configured(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	_, ok := ctx.Value(enabledKey{}).(bool)
	return ok
}

func identifier(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func New(o Options) (*Manager, error) {
	if len(o.Keys) < 1 || len(o.Keys) > 4 || !identifier(o.ActiveKey) {
		return nil, errors.New("draft: configure one to four keys and an active key ID")
	}
	if len(o.Scope) < 1 || len(o.Scope) > 128 {
		return nil, errors.New("draft: scope must contain 1–128 printable ASCII bytes")
	}
	for _, c := range o.Scope {
		if c < 33 || c > 126 {
			return nil, errors.New("draft: invalid scope")
		}
	}
	if o.CookieName == "" {
		o.CookieName = "nextgo_draft"
	}
	if o.Path == "" {
		o.Path = "/"
	}
	if !strings.HasPrefix(o.Path, "/") || strings.HasPrefix(o.Path, "//") || path.Clean(o.Path) != o.Path || strings.ContainsAny(o.Path, ";?%#\\\r\n\t ") {
		return nil, errors.New("draft: cookie path must be a canonical absolute ASCII path")
	}
	for _, c := range o.Path {
		if c < 33 || c > 126 {
			return nil, errors.New("draft: invalid cookie path")
		}
	}
	if o.TTL == 0 {
		o.TTL = time.Hour
	}
	if o.TTL < time.Minute || o.TTL > 24*time.Hour || o.TTL%time.Second != 0 {
		return nil, errors.New("draft: TTL must be whole seconds between one minute and 24 hours")
	}
	m := &Manager{keys: make(map[string][]byte), active: o.ActiveKey, scope: o.Scope, name: o.CookieName, path: o.Path, ttl: o.TTL, secure: !o.AllowInsecure, now: time.Now}
	for id, key := range o.Keys {
		if !identifier(id) || len(key) < 32 || len(key) > 64 {
			return nil, errors.New("draft: invalid key ID or key length")
		}
		m.keys[id] = append([]byte(nil), key...)
	}
	if m.keys[m.active] == nil {
		return nil, errors.New("draft: active key is not configured")
	}
	if strings.HasPrefix(m.name, "__Host-") && (!m.secure || m.path != "/") || strings.HasPrefix(m.name, "__Secure-") && !m.secure {
		return nil, errors.New("draft: cookie prefix conflicts with deployment policy")
	}
	if err := m.cookie("", m.now(), false).Valid(); err != nil {
		return nil, fmt.Errorf("draft: invalid cookie configuration: %w", err)
	}
	return m, nil
}

func (m *Manager) cookie(value string, now time.Time, clear bool) *http.Cookie {
	c := &http.Cookie{Name: m.name, Value: value, Path: m.path, HttpOnly: true, Secure: m.secure, SameSite: http.SameSiteLaxMode, MaxAge: int(m.ttl / time.Second), Expires: now.Add(m.ttl).UTC()}
	if clear {
		c.Value = ""
		c.MaxAge = -1
		c.Expires = time.Unix(1, 0).UTC()
	}
	return c
}

func (m *Manager) signature(key []byte, host, signed string) []byte {
	h := hmac.New(sha256.New, key)
	policy := "secure-cookie"
	if !m.secure {
		policy = "local-http-cookie"
	}
	for _, value := range []string{"nextgo-draft-v1", m.scope, m.name, m.path, policy, host, signed} {
		h.Write([]byte(value))
		h.Write([]byte{0})
	}
	return h.Sum(nil)
}

// Cookie issues a host-bound session after the caller has authenticated and
// authorized preview access. It does not publish a response or authorize a
// request. Use an authenticated POST/CSRF policy and a trusted local redirect.
// No user identity, CMS token or application data is serialized in the cookie.
func (m *Manager) Cookie(r *http.Request) (*http.Cookie, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errors.New("draft: request required")
	}
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	host, err := paths.CanonicalHost(r.Host)
	if err != nil {
		return nil, errors.New("draft: invalid request host")
	}
	now := m.now().UTC().Truncate(time.Second)
	if now.Unix() < 1 {
		return nil, errors.New("draft: invalid clock")
	}
	payload := make([]byte, 33)
	payload[0] = 1
	binary.BigEndian.PutUint64(payload[1:9], uint64(now.Unix()))
	binary.BigEndian.PutUint64(payload[9:17], uint64(now.Add(m.ttl).Unix()))
	if _, err := rand.Read(payload[17:]); err != nil {
		return nil, fmt.Errorf("draft: session entropy: %w", err)
	}
	signed := m.active + "." + base64.RawURLEncoding.EncodeToString(payload)
	return m.cookie(signed+"."+base64.RawURLEncoding.EncodeToString(m.signature(m.keys[m.active], host, signed)), now, false), nil
}

// ClearCookie revokes this browser's cookie. Stateless copies remain valid
// until expiry or key/scope rotation; this is not a durable revocation service.
func (m *Manager) ClearCookie() *http.Cookie {
	if m.Validate() != nil {
		return nil
	}
	return m.cookie("", m.now(), true)
}

func (m *Manager) valid(r *http.Request) bool {
	if m.Validate() != nil || r == nil || r.URL == nil {
		return false
	}
	if m.path != "/" && r.URL.Path != m.path && !strings.HasPrefix(r.URL.Path, m.path+"/") {
		return false
	}
	host, err := paths.CanonicalHost(r.Host)
	if err != nil {
		return false
	}
	var token string
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == m.name {
			count++
			token = cookie.Value
		}
	}
	if count != 1 || len(token) > 256 {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	key := m.keys[parts[0]]
	if key == nil {
		return false
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil || len(payload) != 33 || payload[0] != 1 {
		return false
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || !hmac.Equal(signature, m.signature(key, host, parts[0]+"."+parts[1])) {
		return false
	}
	issued, expires := binary.BigEndian.Uint64(payload[1:9]), binary.BigEndian.Uint64(payload[9:17])
	now := m.now().Unix()
	if now < 1 || issued < 1 || issued > uint64(now)+30 || expires <= uint64(now) || expires <= issued || expires-issued > uint64(m.ttl/time.Second) {
		return false
	}
	return true
}

// BindRequest installs verified state and data-cache bypass. App uses this
// before middleware, route handlers, actions, loaders and completion work.
func (m *Manager) BindRequest(r *http.Request) *http.Request {
	enabled := m.valid(r)
	ctx := context.WithValue(r.Context(), enabledKey{}, enabled)
	if enabled {
		ctx = cache.WithBypass(ctx)
	}
	return r.WithContext(ctx)
}
