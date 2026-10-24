// Package shared provides an explicit serialized cache for multiple application
// instances. Backend operations must be linearizable within a namespace. This
// package has no local value cache and does not silently fall back on outages.
package shared

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	_ "github.com/Suhaibinator/Next.go/serveronly"
)

const (
	// MaxValueBytes bounds one serialized value, before HTTP's base64 encoding.
	MaxValueBytes = 1 << 20
	maxDuration   = 365 * 24 * time.Hour
	// Never disables hard expiration. Eviction and invalidation still apply.
	Never time.Duration = -1
)

var (
	ErrInvalid     = errors.New("shared cache: invalid operation")
	ErrCapacity    = errors.New("shared cache: capacity exhausted")
	ErrPolicy      = errors.New("shared cache: key has a different policy")
	ErrUnavailable = errors.New("shared cache: backend unavailable")
	ErrProtocol    = errors.New("shared cache: incompatible protocol")
	ErrLoader      = errors.New("shared cache: loader panicked")
)

// ID separates application/tenant identity, serialization/build schema and key.
// Namespace and Schema use 1..128 ASCII letters, digits, dots, dashes or
// underscores. Key is 1..4096 UTF-8 bytes without NUL. Include every input which
// affects the value; schema changes must never reuse incompatible JSON types.
type ID struct{ Namespace, Schema, Key string }

// Policy measures freshness and hard expiry from successful Commit, on the
// backend's clock. Positive ExpireAfter must be at least FreshFor, or be Never.
// Equal windows implement a TTL with no stale interval. Readers block
// for a new generation once stale unless Lookup receives a refresh scheduler.
// Every caller of an ID must use the same normalized policy.
type Policy struct {
	FreshFor, ExpireAfter time.Duration
	Tags, Paths           []string
}

// Validate checks the backend's lifetime and label admission bounds.
func (p Policy) Validate() error { _, err := normalize(p); return err }

// View is selected atomically by the backend. Durations are relative to that
// selection, never timestamps from an application server's clock. A granted
// Lease fences Commit/Release; Busy means another unexpired lease owns loading.
// Found may describe a stale value during a lease. BeginStale also includes
// that value while another lease owns refresh; ordinary Begin omits busy data.
// Implementations return owned bytes, not buffers callers can mutate in storage.
type View struct {
	Found                    bool
	Value                    []byte
	Age, FreshFor, ExpiresIn time.Duration
	NoExpiry                 bool
	Lease                    string
	LeaseFor                 time.Duration
	Busy                     bool
}

// Selector expires keys across every schema in one Namespace. Kind is key, tag,
// path, prefix or all. Prefix matches complete path segments. Paths are decoded
// URL labels, normalized without query/fragment. All requires an empty Value.
type Selector struct{ Kind, Value string }

// Validate checks selector bounds independently of an application's namespace.
func (s Selector) Validate() error { _, err := normalizeSelector("validation", s); return err }

// Backend is a fenced read-through protocol. Begin atomically selects a fresh
// value, grants a unique bounded lease for a stale/missing value, or reports Busy.
// Commit accepts only the current unexpired lease, starts generation ages at
// acceptance and returns its selected View. Rejected Commit returns a zero View.
// Release only retires the matching lease; Expire also retires matching leases,
// including keys without a published value. Eviction/restart must invalidate old
// tokens: a token must never become valid again, even after losing all storage.
// Calls respect context cancellation, but a canceled/failed transport can have
// an unknown outcome. Expire is idempotent and callers may retry it. A successful
// Expire cannot recall a read already selected by an earlier operation.
type Backend interface {
	Begin(context.Context, ID, Policy, time.Duration) (View, error)
	// BeginStale is Begin with an unexpired stale value included on Busy. It
	// never serves past hard expiry and grants at most one lease across replicas.
	BeginStale(context.Context, ID, Policy, time.Duration) (View, error)
	Commit(context.Context, ID, string, []byte) (View, error)
	Release(context.Context, ID, string) error
	// Discard retires the matching token and its last-good value. Like Release,
	// it may retire an expired token, but must not touch a replacement owner or
	// committed generation. Transport failure has an unknown remote outcome.
	Discard(context.Context, ID, string) error
	Expire(context.Context, string, Selector) (int, error)
	// MarkStale retires matching leases across schemas and clips the last-good
	// deadline to min(existing, now+window), without resetting generation age.
	// Never preserves the existing deadline; zero is strict Expire. Invalid
	// windows must leave storage unchanged. Missing/expired values are removed.
	MarkStale(context.Context, string, Selector, time.Duration) (int, error)
}

func validWindow(window time.Duration) bool {
	return window == Never || window >= 0 && window <= maxDuration
}

func validName(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func validText(s string, limit int) bool {
	return s != "" && len(s) <= limit && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func validateID(id ID) error {
	if !validName(id.Namespace) || !validName(id.Schema) || !validText(id.Key, 4096) {
		return ErrInvalid
	}
	return nil
}
func normalize(p Policy) (Policy, error) {
	if p.FreshFor < 0 || p.FreshFor > maxDuration || p.ExpireAfter != Never && (p.ExpireAfter <= 0 || p.ExpireAfter < p.FreshFor || p.ExpireAfter > maxDuration) || len(p.Tags) > 64 || len(p.Paths) > 64 {
		return Policy{}, ErrInvalid
	}
	p.Tags, p.Paths = slices.Clone(p.Tags), slices.Clone(p.Paths)
	for _, list := range [][]string{p.Tags, p.Paths} {
		for _, s := range list {
			if !validText(s, 512) {
				return Policy{}, ErrInvalid
			}
		}
	}
	for i, s := range p.Paths {
		p.Paths[i] = cleanPath(s)
	}
	slices.Sort(p.Tags)
	p.Tags = slices.Compact(p.Tags)
	slices.Sort(p.Paths)
	p.Paths = slices.Compact(p.Paths)
	return p, nil
}
func equalPolicy(a, b Policy) bool {
	return a.FreshFor == b.FreshFor && a.ExpireAfter == b.ExpireAfter && slices.Equal(a.Tags, b.Tags) && slices.Equal(a.Paths, b.Paths)
}
func cleanPath(s string) string {
	s, _, _ = strings.Cut(s, "?")
	s, _, _ = strings.Cut(s, "#")
	return path.Clean("/" + strings.TrimPrefix(s, "/"))
}
func normalizeSelector(ns string, s Selector) (Selector, error) {
	if !validName(ns) {
		return Selector{}, ErrInvalid
	}
	switch s.Kind {
	case "all":
		if s.Value != "" {
			return Selector{}, ErrInvalid
		}
	case "key":
		if !validText(s.Value, 4096) {
			return Selector{}, ErrInvalid
		}
	case "tag":
		if !validText(s.Value, 512) {
			return Selector{}, ErrInvalid
		}
	case "path", "prefix":
		if !validText(s.Value, 512) {
			return Selector{}, ErrInvalid
		}
		s.Value = cleanPath(s.Value)
	default:
		return Selector{}, ErrInvalid
	}
	return s, nil
}
func validateLease(d time.Duration) error {
	if d < 100*time.Millisecond || d > time.Minute {
		return fmt.Errorf("%w: lease must be 100ms..1m", ErrInvalid)
	}
	return nil
}
func validToken(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
