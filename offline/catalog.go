package offline

import (
	"crypto/sha256"
	"sort"
	"strconv"
	"strings"
)

// NativeAdmissionV1 names the future native final-response gate. Merely putting
// this value in a catalog never establishes that the gate ran. The worker must
// independently validate the reserved native protocol on each prepared response.
const NativeAdmissionV1 = "buffered-anonymous-document-v1"

type AssetKind string

const (
	WorkerWASM  AssetKind = "worker-wasm"
	CoreWASM    AssetKind = "core-wasm"
	RuntimeShim AssetKind = "runtime-shim"
	BootScript  AssetKind = "boot-script"
	LazyWASM    AssetKind = "lazy-wasm"
	Style       AssetKind = "style"
	PublicAsset AssetKind = "public-asset"
)

// Every listed asset is required. The authoritative compiler inventory must
// include all hydration dependencies; this package cannot discover hidden
// application dependencies from a claim or from HTML.
type Asset struct {
	Kind         AssetKind
	URL          string
	SHA256       string
	DecodedBytes int64
	ContentType  string
}

// Document describes a fully buffered, native-approved anonymous HTML body.
// URL queries and representation variants are not supported. The fallback must
// be a self-contained public document that cannot hydrate a different page;
// proving that condition belongs to the future native gate, not this struct.
type Document struct {
	Path         string
	SHA256       string
	DecodedBytes int64
}

type CatalogSpec struct {
	Version       int
	Config        Config
	Admission     string
	RuntimeID     string
	Configuration string
	SavedAtUnix   int64
	Assets        []Asset
	Documents     []Document
}

type Entry struct {
	URL          string
	Kind         string
	SHA256       string
	DecodedBytes int64
	ContentType  string
}

// Cost reserves full decoded payloads and complete metadata-record allowances,
// including staging data. Shared URLs are charged separately per generation.
type Cost struct {
	Bytes   int64
	Entries int
}

type Catalog struct {
	policy         *Policy
	spec           CatalogSpec
	entries        []Entry
	generation     string
	policyIdentity string
	cost           Cost
}

func NewCatalog(spec CatalogSpec) (*Catalog, error) {
	p, err := Normalize(spec.Config)
	if err != nil {
		return nil, err
	}
	if spec.Version != Version || spec.Admission != NativeAdmissionV1 || !lowerHex(spec.RuntimeID, 20) || !lowerHex(spec.Configuration, 64) || spec.SavedAtUnix < 1 || spec.SavedAtUnix > 1<<53-int64(MaxAge.Seconds()) || len(spec.Assets) == 0 || len(spec.Assets) > MaxAssets || len(spec.Documents) != len(p.config.Pages) {
		return nil, ErrInvalid
	}
	spec.Config = p.Config()
	spec.RuntimeID, spec.Configuration = strings.Clone(spec.RuntimeID), strings.Clone(spec.Configuration)
	spec.Assets = append([]Asset(nil), spec.Assets...)
	spec.Documents = append([]Document(nil), spec.Documents...)
	sort.Slice(spec.Assets, func(i, j int) bool { return spec.Assets[i].URL < spec.Assets[j].URL })
	sort.Slice(spec.Documents, func(i, j int) bool { return spec.Documents[i].Path < spec.Documents[j].Path })
	c := &Catalog{policy: p, spec: spec, cost: Cost{Bytes: MaxCatalogBytes + MaxCommitBytes, Entries: 2}}
	counts := map[AssetKind]int{}
	for i, asset := range spec.Assets {
		if !assetValid(p, spec.RuntimeID, asset) || i > 0 && spec.Assets[i-1].URL == asset.URL {
			return nil, ErrInvalid
		}
		asset.URL, asset.SHA256, asset.ContentType = strings.Clone(asset.URL), strings.Clone(asset.SHA256), strings.Clone(asset.ContentType)
		c.spec.Assets[i] = asset
		counts[asset.Kind]++
		c.entries = append(c.entries, Entry{asset.URL, string(asset.Kind), asset.SHA256, asset.DecodedBytes, asset.ContentType})
		c.cost.Bytes += asset.DecodedBytes
		c.cost.Entries++
	}
	for _, kind := range []AssetKind{WorkerWASM, CoreWASM, RuntimeShim, BootScript} {
		if counts[kind] != 1 {
			return nil, ErrIncomplete
		}
	}
	if counts[Style] == 0 {
		return nil, ErrIncomplete
	}
	for i, doc := range spec.Documents {
		if doc.Path != p.config.Pages[i].Path || !lowerHex(doc.SHA256, 64) || doc.DecodedBytes < 1 || doc.DecodedBytes > p.config.Budget.DocumentBytes {
			return nil, ErrInvalid
		}
		doc.Path, doc.SHA256 = strings.Clone(doc.Path), strings.Clone(doc.SHA256)
		c.spec.Documents[i] = doc
		kind := "document"
		if doc.Path == p.config.Fallback {
			kind = "fallback"
		}
		c.entries = append(c.entries, Entry{p.documentURL(doc.Path), kind, doc.SHA256, doc.DecodedBytes, "text/html; charset=utf-8"})
		c.cost.Bytes += doc.DecodedBytes
		c.cost.Entries++
	}
	if c.cost.Bytes > p.config.Budget.GenerationBytes || c.cost.Bytes > p.config.Budget.TotalBytes || c.cost.Entries > p.config.Budget.GenerationEntries || c.cost.Entries > p.config.Budget.Entries {
		return nil, ErrQuota
	}
	sort.Slice(c.entries, func(i, j int) bool { return c.entries[i].URL < c.entries[j].URL })
	policy := policyCanonical(p.config)
	canonical := c.canonical(policy)
	if len(canonical) > MaxCatalogBytes {
		return nil, ErrQuota
	}
	c.policyIdentity = digest(policy)
	c.generation = digest(canonical)
	return c, nil
}

func assetValid(p *Policy, runtime string, asset Asset) bool {
	if !lowerHex(asset.SHA256, 64) || asset.DecodedBytes < 1 || asset.DecodedBytes > p.config.Budget.AssetBytes || len(asset.URL) > MaxURLBytes {
		return false
	}
	prefix := p.config.Origin + p.config.BasePath + "/_nextgo/" + runtime + "/"
	if !strings.HasPrefix(asset.URL, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(asset.URL, prefix)
	if !literalPath("/"+suffix, false, false) {
		return false
	}
	switch asset.Kind {
	case WorkerWASM:
		return suffix == "worker-"+asset.SHA256+".wasm" && asset.ContentType == "application/wasm"
	case CoreWASM:
		return suffix == "app.wasm" && asset.ContentType == "application/wasm"
	case RuntimeShim:
		return suffix == "wasm_exec.js" && scriptMIME(asset.ContentType)
	case BootScript:
		return suffix == "boot.js" && scriptMIME(asset.ContentType)
	case LazyWASM:
		return suffix == "module-"+asset.SHA256+".wasm" && asset.ContentType == "application/wasm"
	case Style:
		return strings.HasPrefix(suffix, "offline-public/") && strings.HasSuffix(suffix, ".css") && asset.ContentType == "text/css; charset=utf-8"
	case PublicAsset:
		if !strings.HasPrefix(suffix, "offline-public/") {
			return false
		}
		for _, match := range [][2]string{{".png", "image/png"}, {".ico", "image/x-icon"}, {".jpg", "image/jpeg"}, {".jpeg", "image/jpeg"}, {".webp", "image/webp"}, {".woff", "font/woff"}, {".woff2", "font/woff2"}} {
			if strings.HasSuffix(suffix, match[0]) && asset.ContentType == match[1] {
				return true
			}
		}
	}
	return false
}

func scriptMIME(value string) bool {
	return value == "text/javascript; charset=utf-8" || value == "application/javascript"
}

func (c *Catalog) Generation() string {
	if c == nil {
		return ""
	}
	return c.generation
}
func (c *Catalog) Cost() Cost {
	if c == nil {
		return Cost{}
	}
	return c.cost
}
func (c *Catalog) Policy() *Policy {
	if c == nil {
		return nil
	}
	return c.policy
}
func (c *Catalog) Entries() []Entry {
	if c == nil {
		return nil
	}
	return append([]Entry(nil), c.entries...)
}
func (c *Catalog) Spec() CatalogSpec {
	if c == nil {
		return CatalogSpec{}
	}
	spec := c.spec
	spec.Config = c.policy.Config()
	spec.Assets = append([]Asset(nil), spec.Assets...)
	spec.Documents = append([]Document(nil), spec.Documents...)
	return spec
}

// PreparationURL addresses exact frozen bytes through the dedicated native
// transport, separate from the original page URL and its HTTP/CDN cache key.
// The worker must still check native approval, generation, document hash and
// compatibility before saving those bytes under the original Entry.URL.
// A no-store transport response is not application public-data admission.
func (c *Catalog) PreparationURL(path string) (string, error) {
	if c == nil || c.policy == nil || c.policy.config.ID == "" || !lowerHex(c.generation, 64) {
		return "", ErrInvalid
	}
	for _, doc := range c.spec.Documents {
		if doc.Path == path {
			value := c.policy.config.Origin + c.policy.config.BasePath + PreparedDocumentsPath + c.generation + "/" + doc.SHA256
			if len(value) > MaxURLBytes {
				return "", ErrInvalid
			}
			return value, nil
		}
	}
	return "", ErrInvalid
}

// Fresh requires the adapter's persisted highest observed wall-clock time. The
// adapter must restore/update that fence across worker restarts; this stateless
// predicate cannot discover a rollback by itself. It never extends expiry.
func (c *Catalog) Fresh(path string, nowUnix, highestObservedUnix int64) bool {
	if c == nil || c.policy == nil || nowUnix < c.spec.SavedAtUnix || highestObservedUnix < c.spec.SavedAtUnix || nowUnix < highestObservedUnix {
		return false
	}
	for _, page := range c.policy.config.Pages {
		if page.Path == path {
			return nowUnix < c.spec.SavedAtUnix+int64(page.MaxAge.Seconds())
		}
	}
	return false
}

// Observation must describe fully read, decoded bytes hashed by the adapter.
// Neither an HTTP cache header nor a commit marker is an observation.
type Observation struct {
	URL, SHA256  string
	DecodedBytes int64
	ContentType  string
}

func (c *Catalog) VerifyInventory(observed []Observation) error {
	if c == nil || c.policy == nil || c.generation == "" || len(observed) != len(c.entries) {
		return ErrIncomplete
	}
	seen := make(map[string]bool, len(observed))
	for _, item := range observed {
		if seen[item.URL] || !c.matches(item) {
			return ErrIncomplete
		}
		seen[item.URL] = true
	}
	return nil
}

func (c *Catalog) matches(item Observation) bool {
	i := sort.Search(len(c.entries), func(i int) bool { return c.entries[i].URL >= item.URL })
	if i == len(c.entries) {
		return false
	}
	want := c.entries[i]
	return item.URL == want.URL && item.SHA256 == want.SHA256 && item.DecodedBytes == want.DecodedBytes && item.ContentType == want.ContentType
}

// Length-prefixing makes the canonical identity independent of JSON encoding,
// input enumeration order, wall-clock formatting and delimiter-like values.
func field(b *strings.Builder, value string) {
	b.WriteString(strconv.Itoa(len(value)))
	b.WriteByte(':')
	b.WriteString(value)
}
func integer(b *strings.Builder, value int64) { field(b, strconv.FormatInt(value, 10)) }
func policyCanonical(c Config) string {
	var b strings.Builder
	field(&b, "nextgo.offline.policy.v1")
	for _, value := range []string{c.ID, c.Origin, c.BasePath, string(c.Semantics), c.Fallback, string(c.Updates)} {
		field(&b, value)
	}
	for _, n := range []int64{c.Budget.TotalBytes, c.Budget.GenerationBytes, c.Budget.AssetBytes, c.Budget.DocumentBytes, int64(c.Budget.Entries), int64(c.Budget.GenerationEntries), int64(c.Budget.Generations), int64(c.Budget.InFlight), int64(c.Budget.Queue), int64(len(c.Pages))} {
		integer(&b, n)
	}
	for _, page := range c.Pages {
		field(&b, page.Path)
		integer(&b, int64(page.MaxAge))
	}
	return b.String()
}
func (c *Catalog) canonical(policy string) string {
	var b strings.Builder
	field(&b, "nextgo.offline.catalog.v1")
	field(&b, policy)
	for _, value := range []string{c.spec.Admission, c.spec.RuntimeID, c.spec.Configuration} {
		field(&b, value)
	}
	integer(&b, c.spec.SavedAtUnix)
	integer(&b, int64(len(c.entries)))
	for _, entry := range c.entries {
		for _, value := range []string{entry.URL, entry.Kind, entry.SHA256, entry.ContentType} {
			field(&b, value)
		}
		integer(&b, entry.DecodedBytes)
	}
	return b.String()
}
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	const alphabet = "0123456789abcdef"
	var output [64]byte
	for i, b := range sum {
		output[i*2], output[i*2+1] = alphabet[b>>4], alphabet[b&15]
	}
	return string(output[:])
}
