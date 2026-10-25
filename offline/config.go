// Package offline defines the bounded saved-anonymous-public document contract.
// It performs no network, filesystem, browser or application operations. Policy
// declarations are not proof that a response is public: a future native final
// admission gate must approve each complete anonymous representation.
package offline

import (
	"errors"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalid    = errors.New("offline: invalid contract")
	ErrQuota      = errors.New("offline: reservation exceeds budget")
	ErrIncomplete = errors.New("offline: incomplete or corrupt inventory")
	ErrState      = errors.New("offline: invalid generation transition")
)

const (
	Version         = 1
	MaxPages        = 16
	MaxAssets       = 128
	MaxURLBytes     = 2048
	MaxCatalogBytes = 64 << 10
	MaxCommitBytes  = 1024
	MaxAge          = 7 * 24 * time.Hour
	GenerationSlots = 3 // Active, previous and staging are all charged.
)

type Semantics string

const SavedAnonymousPublic Semantics = "saved-anonymous-public"

type UpdateMode string

const WaitForClosedClients UpdateMode = "wait-for-closed-clients"

// Page is an app-relative literal, query-free document path. MaxAge is the
// explicit offline retention permission, independent of HTTP cache lifetime.
type Page struct {
	Path   string
	MaxAge time.Duration
}

// Budget includes every active, previous and staging payload plus the reserved
// catalog/commit record allowances. It is not an RSS or browser quota promise.
type Budget struct {
	TotalBytes        int64
	GenerationBytes   int64
	AssetBytes        int64
	DocumentBytes     int64
	Entries           int
	GenerationEntries int
	Generations       int
	InFlight          int
	Queue             int
}

type Config struct {
	ID        string
	Origin    string // Trusted, exact canonical origin, never inferred from requests.
	BasePath  string
	Semantics Semantics // Required explicit public-data declaration.
	Pages     []Page
	Fallback  string // Must also appear in Pages with explicit retention permission.
	Budget    Budget
	Updates   UpdateMode
}

// Policy is immutable. No Config slice or caller-owned buffer is retained.
type Policy struct{ config Config }

func Normalize(config Config) (*Policy, error) {
	if !identifier(config.ID, 64) || !literalOrigin(config.Origin) || !basePath(config.BasePath) || config.Semantics != SavedAnonymousPublic {
		return nil, ErrInvalid
	}
	if config.Updates == "" {
		config.Updates = WaitForClosedClients
	}
	if config.Updates != WaitForClosedClients || len(config.Pages) == 0 || len(config.Pages) > MaxPages {
		return nil, ErrInvalid
	}
	budget, err := normalizeBudget(config.Budget)
	if err != nil {
		return nil, err
	}
	config.Budget = budget
	pages := append([]Page(nil), config.Pages...)
	sort.Slice(pages, func(i, j int) bool { return pages[i].Path < pages[j].Path })
	fallback := false
	for i, page := range pages {
		if !pagePath(page.Path) || len(config.Origin)+len(config.BasePath)+len(page.Path) > MaxURLBytes || page.MaxAge < time.Second || page.MaxAge > MaxAge || page.MaxAge%time.Second != 0 || i > 0 && pages[i-1].Path == page.Path {
			return nil, ErrInvalid
		}
		fallback = fallback || page.Path == config.Fallback
	}
	if !fallback {
		return nil, ErrInvalid
	}
	config.ID, config.Origin, config.BasePath = strings.Clone(config.ID), strings.Clone(config.Origin), strings.Clone(config.BasePath)
	config.Fallback = strings.Clone(config.Fallback)
	for i := range pages {
		pages[i].Path = strings.Clone(pages[i].Path)
	}
	config.Pages = pages
	return &Policy{config: config}, nil
}

func normalizeBudget(b Budget) (Budget, error) {
	if b.TotalBytes == 0 {
		b.TotalBytes = 96 << 20
	}
	if b.GenerationBytes == 0 {
		b.GenerationBytes = 32 << 20
	}
	if b.AssetBytes == 0 {
		b.AssetBytes = 16 << 20
	}
	if b.DocumentBytes == 0 {
		b.DocumentBytes = 512 << 10
	}
	if b.Entries == 0 {
		b.Entries = 3 * 160
	}
	if b.GenerationEntries == 0 {
		b.GenerationEntries = 160
	}
	if b.Generations == 0 {
		b.Generations = GenerationSlots
	}
	if b.InFlight == 0 {
		b.InFlight = 8
	}
	if b.Queue == 0 {
		b.Queue = 16
	}
	if b.TotalBytes < 1 || b.TotalBytes > 256<<20 || b.GenerationBytes < 1 || b.GenerationBytes > b.TotalBytes || b.AssetBytes < 1 || b.AssetBytes > 32<<20 || b.AssetBytes > b.GenerationBytes || b.DocumentBytes < 1 || b.DocumentBytes > 2<<20 || b.DocumentBytes > b.GenerationBytes || b.Entries < 1 || b.Entries > 3*1024 || b.GenerationEntries < 1 || b.GenerationEntries > b.Entries || b.Generations != GenerationSlots || b.InFlight < 1 || b.InFlight > 32 || b.Queue < 1 || b.Queue > 64 {
		return b, ErrInvalid
	}
	return b, nil
}

func (p *Policy) Config() Config {
	if p == nil {
		return Config{}
	}
	copy := p.config
	copy.Pages = append([]Page(nil), copy.Pages...)
	return copy
}

func (p *Policy) documentURL(path string) string {
	if path == "/" && p.config.BasePath != "" {
		return p.config.Origin + p.config.BasePath
	}
	return p.config.Origin + p.config.BasePath + path
}
func (p *Policy) owner() string {
	return p.config.Origin + "\x00" + p.config.BasePath + "\x00" + p.config.ID
}

func identifier(value string, limit int) bool {
	if value == "" || len(value) > limit {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func lowerHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// Narrow literal grammar deliberately excludes percent aliases, queries,
// fragments, duplicate separators, dot segments and non-ASCII spelling.
func literalPath(value string, root, trailing bool) bool {
	if value == "/" {
		return root
	}
	if value == "" || len(value) > MaxURLBytes || value[0] != '/' || strings.Contains(value, "//") {
		return false
	}
	if strings.HasSuffix(value, "/") {
		if !trailing {
			return false
		}
		value = strings.TrimSuffix(value, "/")
	}
	for _, part := range strings.Split(value[1:], "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)) {
				return false
			}
		}
	}
	return true
}

func reserved(value string) bool {
	first, _, _ := strings.Cut(strings.TrimPrefix(value, "/"), "/")
	switch strings.ToLower(first) {
	case "api", "assets", "_nextgo", "nextgo-sw.js", "robots.txt", "sitemap.xml", "favicon.ico", "icon.png", "apple-icon.png", "manifest.webmanifest":
		return true
	}
	return false
}

func pagePath(value string) bool { return literalPath(value, true, false) && !reserved(value) }
func basePath(value string) bool {
	return value == "" || literalPath(value, false, false) && !reserved(value)
}

func literalOrigin(value string) bool {
	if len(value) == 0 || len(value) > 512 {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.User != nil || u.Opaque != "" || value != u.Scheme+"://"+u.Host || u.Host != strings.ToLower(u.Host) {
		return false
	}
	host := u.Hostname()
	if host == "" || strings.HasSuffix(host, ".") {
		return false
	}
	address, addressErr := netip.ParseAddr(host)
	if addressErr != nil {
		if len(host) > 253 {
			return false
		}
		last := host[strings.LastIndex(host, ".")+1:]
		// Browsers recognize shortened/octal/hex IPv4 spellings which netip
		// intentionally does not. Never accept one as a DNS origin alias.
		if strings.HasPrefix(last, "0x") {
			return false
		}
		numeric := true
		for _, c := range last {
			numeric = numeric && c >= '0' && c <= '9'
		}
		if numeric {
			return false
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return false
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return false
				}
			}
		}
	} else if address.Zone() != "" || address.Is4In6() || address.String() != host {
		return false
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port || u.Scheme == "https" && n == 443 || u.Scheme == "http" && n == 80 {
			return false
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return false
	}
	return u.Scheme == "https" || host == "localhost" || addressErr == nil && address.IsLoopback()
}
