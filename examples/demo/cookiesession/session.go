//go:build !js

// Package cookiesession demonstrates native response cookies and fresh reads.
// It is a stateless, signed, short-lived demo, not a production identity/session
// provider. Logout clears the browser cookie; it does not revoke copied tokens.
// Durable revocation, key rotation, account storage and provider integration are
// deliberately outside this example.
package cookiesession

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/access"
	_ "github.com/Suhaibinator/Next.go/serveronly"
)

const lifetime = 15 * time.Minute
const maxTokenBytes = 1024
const maxCookieHeaderBytes = 16 << 10
const maxScopePathBytes = 4096

var ErrUnavailable = errors.New("cookie session demo is unavailable")

type scope struct {
	name, path, binding string
	secure              bool
}

type claims struct {
	Version int
	Role    access.Role
	Issued  int64
	Expires int64
	Nonce   string
}

// Scope returns only public cookie placement, never signing material.
func Scope(ctx context.Context, r *http.Request) (name, path string, err error) {
	s, err := requestScope(ctx, r)
	return s.name, s.path, err
}

func requestScope(ctx context.Context, r *http.Request) (scope, error) {
	if ctx == nil || r == nil || len(r.Host) == 0 || len(r.Host) > 512 {
		return scope{}, ErrUnavailable
	}
	for _, char := range r.Host {
		if char < 33 || char > 126 || strings.ContainsRune("\\/?#%@", char) {
			return scope{}, ErrUnavailable
		}
	}
	u, err := url.Parse("//" + r.Host)
	if err != nil || u.Host != r.Host || u.User != nil || u.Hostname() == "" {
		return scope{}, ErrUnavailable
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return scope{}, ErrUnavailable
		}
	}
	secure := r.TLS != nil
	if !secure {
		host := strings.ToLower(u.Hostname())
		ip := net.ParseIP(host)
		if os.Getenv("NEXTGO_DEMO_COOKIE_ALLOW_INSECURE") != "true" || host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return scope{}, ErrUnavailable
		}
	}
	mount, err := nextgo.URL(ctx, "/")
	if err != nil || len(mount) > 8192 || !strings.HasPrefix(mount, "/") || strings.ContainsAny(mount, "?#\\\r\n") {
		return scope{}, ErrUnavailable
	}
	// The exact public mount covers its canonical root too. RFC cookie path
	// matching already requires a segment boundary, so /base never matches
	// /base-other; adding a slash would omit the nontrailing /base root.
	path := mount
	if len(path) > maxScopePathBytes {
		return scope{}, ErrUnavailable
	}
	binding := strings.ToLower(r.Host) + "\x00" + mount
	digest := sha256.Sum256([]byte(binding))
	return scope{name: "nextgo_demo_session_" + hex.EncodeToString(digest[:6]), path: path, binding: binding, secure: secure}, nil
}

func signingKey() ([32]byte, error) {
	var key [32]byte
	raw := os.Getenv("NEXTGO_DEMO_COOKIE_KEY")
	if len(raw) != 64 {
		return key, ErrUnavailable
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil || len(decoded) != len(key) {
		return key, ErrUnavailable
	}
	copy(key[:], decoded)
	return key, nil
}

// Available checks native configuration and transport. Forwarded headers do not
// grant TLS. An explicit opt-in permits loopback HTTP only for local testing.
func Available(ctx context.Context, r *http.Request) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	_, keyErr := signingKey()
	_, scopeErr := requestScope(ctx, r)
	return keyErr == nil && scopeErr == nil
}

// Login reuses the existing opaque-credential policy without trusting a role
// input, query string, or caller-provided role header.
func Login(ctx context.Context, r *http.Request, credential string) (access.Role, *http.Cookie, error) {
	if ctx == nil {
		return "", nil, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if !Available(ctx, r) {
		return "", nil, ErrUnavailable
	}
	probe := r.Clone(ctx)
	probe.Header = r.Header.Clone()
	probe.Header.Set("Authorization", "Bearer "+credential)
	role, err := access.Authenticate(probe)
	if err != nil {
		return "", nil, err
	}
	s, err := requestScope(ctx, r)
	if err != nil {
		return "", nil, ErrUnavailable
	}
	key, err := signingKey()
	if err != nil {
		return "", nil, ErrUnavailable
	}
	now := time.Now().UTC()
	var random [16]byte
	if _, err := io.ReadFull(rand.Reader, random[:]); err != nil {
		return "", nil, ErrUnavailable
	}
	value := claims{Version: 1, Role: role, Issued: now.Unix(), Expires: now.Add(lifetime).Unix(), Nonce: base64.RawURLEncoding.EncodeToString(random[:])}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", nil, ErrUnavailable
	}
	payload := base64.RawURLEncoding.EncodeToString(encoded)
	mac := signature(key, s.binding, payload)
	cookie := &http.Cookie{Name: s.name, Value: payload + "." + base64.RawURLEncoding.EncodeToString(mac), Path: s.path, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secure, Expires: time.Unix(value.Expires, 0), MaxAge: int(lifetime.Seconds())}
	return role, cookie, nil
}

func signature(key [32]byte, binding, payload string) []byte {
	mac := hmac.New(sha256.New, key[:])
	_, _ = io.WriteString(mac, "nextgo-demo-session-v1\x00")
	_, _ = io.WriteString(mac, binding)
	_, _ = io.WriteString(mac, "\x00")
	_, _ = io.WriteString(mac, payload)
	return mac.Sum(nil)
}

// Clear clears only this browser scope. A previously copied valid signed token
// remains usable until expiry: this demo has no durable revocation authority.
func Clear(ctx context.Context, r *http.Request) (*http.Cookie, error) {
	if !Available(ctx, r) {
		return nil, ErrUnavailable
	}
	s, err := requestScope(ctx, r)
	if err != nil {
		return nil, err
	}
	return &http.Cookie{Name: s.name, Path: s.path, Value: "", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secure, MaxAge: -1, Expires: time.Unix(1, 0)}, nil
}

// Read verifies the cookie again for every data read. Stateless verification
// works across cold processes using the same configured key and scope.
func Read(ctx context.Context, r *http.Request) (access.Role, error) {
	return readAt(ctx, r, time.Now().UTC())
}

func readAt(ctx context.Context, r *http.Request, now time.Time) (access.Role, error) {
	if ctx == nil {
		return "", nextgo.ErrUnauthorized
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s, err := requestScope(ctx, r)
	if err != nil {
		return "", nextgo.ErrUnauthorized
	}
	key, err := signingKey()
	if err != nil {
		return "", nextgo.ErrUnauthorized
	}
	token, err := requestToken(r, s.name)
	if err != nil {
		return "", nextgo.ErrUnauthorized
	}
	payload, supplied, ok := strings.Cut(token, ".")
	if !ok || strings.Contains(supplied, ".") || len(payload) > 768 || len(supplied) != 43 {
		return "", nextgo.ErrUnauthorized
	}
	mac, err := base64.RawURLEncoding.Strict().DecodeString(supplied)
	if err != nil || !hmac.Equal(mac, signature(key, s.binding, payload)) {
		return "", nextgo.ErrUnauthorized
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(payload)
	if err != nil || len(decoded) > 512 {
		return "", nextgo.ErrUnauthorized
	}
	value, err := decodeClaims(decoded)
	if err != nil || value.Version != 1 || value.Role != access.Reader && value.Role != access.Administrator || value.Issued <= 0 || value.Issued > now.Unix()+30 || value.Expires <= value.Issued || value.Expires-value.Issued > int64(lifetime.Seconds()) || value.Expires <= now.Unix() {
		return "", nextgo.ErrUnauthorized
	}
	nonce, err := base64.RawURLEncoding.Strict().DecodeString(value.Nonce)
	if err != nil || len(nonce) != 16 {
		return "", nextgo.ErrUnauthorized
	}
	return value.Role, nil
}

func requestToken(r *http.Request, name string) (string, error) {
	count, pairs, total := 0, 0, 0
	var token string
	for _, header := range r.Header.Values("Cookie") {
		total += len(header)
		if total > maxCookieHeaderBytes {
			return "", nextgo.ErrUnauthorized
		}
		for _, pair := range strings.Split(header, ";") {
			pairs++
			if pairs > 128 {
				return "", nextgo.ErrUnauthorized
			}
			key, value, ok := strings.Cut(strings.TrimSpace(pair), "=")
			if strings.TrimSpace(key) != name {
				continue
			}
			count++
			if !ok || count != 1 || len(value) == 0 || len(value) > maxTokenBytes || strings.ContainsAny(value, "\"' \\\t\r\n") {
				return "", nextgo.ErrUnauthorized
			}
			token = value
		}
	}
	if count != 1 {
		return "", nextgo.ErrUnauthorized
	}
	return token, nil
}

func decodeClaims(data []byte) (claims, error) {
	var value claims
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return value, nextgo.ErrUnauthorized
	}
	seen := make(map[string]bool, 5)
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return value, nextgo.ErrUnauthorized
		}
		seen[name] = true
		switch name {
		case "Version":
			err = decoder.Decode(&value.Version)
		case "Role":
			err = decoder.Decode(&value.Role)
		case "Issued":
			err = decoder.Decode(&value.Issued)
		case "Expires":
			err = decoder.Decode(&value.Expires)
		case "Nonce":
			err = decoder.Decode(&value.Nonce)
		default:
			return value, nextgo.ErrUnauthorized
		}
		if err != nil {
			return value, nextgo.ErrUnauthorized
		}
	}
	end, err := decoder.Token()
	var trailing any
	if err != nil || end != json.Delim('}') || len(seen) != 5 || decoder.Decode(&trailing) != io.EOF {
		return value, nextgo.ErrUnauthorized
	}
	return value, nil
}

// ReadReport checks the signed identity anew at the data boundary. A caller's
// query, role header, or authorization bearer alone never grants this access.
func ReadReport(ctx context.Context, r *http.Request) (access.Report, error) {
	role, err := Read(ctx, r)
	if err != nil {
		return access.Report{}, err
	}
	if role != access.Administrator {
		return access.Report{}, nextgo.ErrForbidden
	}
	return access.Report{Title: "Signed-session administrator report", Summary: "The server verified this request's signed administrator cookie before loading this report."}, nil
}
