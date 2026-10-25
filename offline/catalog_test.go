package offline

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func sampleConfig() Config {
	return Config{ID: "public-guide", Origin: "https://example.test", BasePath: "/guide", Semantics: SavedAnonymousPublic,
		Pages: []Page{{Path: "/", MaxAge: 6 * time.Hour}, {Path: "/offline", MaxAge: time.Hour}}, Fallback: "/offline"}
}

func sampleSpec(saved int64) CatalogSpec {
	c := sampleConfig()
	runtime := strings.Repeat("a", 20)
	prefix := c.Origin + c.BasePath + "/_nextgo/" + runtime + "/"
	sha := strings.Repeat("b", 64)
	return CatalogSpec{Version: Version, Config: c, Admission: NativeAdmissionV1, RuntimeID: runtime, Configuration: strings.Repeat("c", 64), SavedAtUnix: saved,
		Assets: []Asset{
			{CoreWASM, prefix + "app.wasm", sha, 8401692, "application/wasm"},
			{WorkerWASM, prefix + "worker-" + sha + ".wasm", sha, 128, "application/wasm"},
			{RuntimeShim, prefix + "wasm_exec.js", sha, 8192, "text/javascript; charset=utf-8"},
			{BootScript, prefix + "boot.js", sha, 64, "text/javascript; charset=utf-8"},
			{Style, prefix + "offline-public/style.css", sha, 128, "text/css; charset=utf-8"},
			{LazyWASM, prefix + "module-" + sha + ".wasm", sha, 256, "application/wasm"},
		}, Documents: []Document{{"/", sha, 128}, {"/offline", sha, 256}}}
}

func mustCatalog(t *testing.T, spec CatalogSpec) *Catalog {
	t.Helper()
	c, err := NewCatalog(spec)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func observations(c *Catalog) []Observation {
	var result []Observation
	for _, e := range c.Entries() {
		result = append(result, Observation{e.URL, e.SHA256, e.DecodedBytes, e.ContentType})
	}
	return result
}

func TestPolicyRejectsAmbiguousOrUnsupportedInputs(t *testing.T) {
	cases := map[string]func(*Config){
		"implicit-public":     func(c *Config) { c.Semantics = "" },
		"foreign-http":        func(c *Config) { c.Origin = "http://example.test" },
		"origin-path":         func(c *Config) { c.Origin += "/" },
		"origin-query":        func(c *Config) { c.Origin += "?private=1" },
		"origin-user":         func(c *Config) { c.Origin = "https://user@example.test" },
		"origin-case":         func(c *Config) { c.Origin = "https://Example.test" },
		"origin-default-port": func(c *Config) { c.Origin += ":443" },
		"origin-short-ip":     func(c *Config) { c.Origin = "https://127.1" },
		"origin-octal-ip":     func(c *Config) { c.Origin = "https://0177.0.0.1" },
		"origin-hex-ip":       func(c *Config) { c.Origin = "https://0x7f000001" },
		"origin-ipv4-mapped":  func(c *Config) { c.Origin = "https://[::ffff:127.0.0.1]" },
		"base-encoded":        func(c *Config) { c.BasePath = "/%67uide" },
		"base-slash":          func(c *Config) { c.BasePath = "/guide/" },
		"page-query":          func(c *Config) { c.Pages[0].Path = "/?token=x" },
		"page-encoded":        func(c *Config) { c.Pages[0].Path = "/%61bout" },
		"page-dot":            func(c *Config) { c.Pages[0].Path = "/a/../b" },
		"page-slash":          func(c *Config) { c.Pages[0].Path = "/a//b" },
		"page-reserved":       func(c *Config) { c.Pages[0].Path = "/_nextgo/actions/change" },
		"page-api":            func(c *Config) { c.Pages[0].Path = "/api/account" },
		"page-runtime":        func(c *Config) { c.Pages[0].Path = "/assets/a.png" },
		"page-metadata":       func(c *Config) { c.Pages[0].Path = "/manifest.webmanifest" },
		"page-wildcard":       func(c *Config) { c.Pages[0].Path = "/posts/*slug" },
		"page-duplicate":      func(c *Config) { c.Pages = append(c.Pages, c.Pages[0]) },
		"fallback-undeclared": func(c *Config) { c.Fallback = "/private" },
		"age-zero":            func(c *Config) { c.Pages[0].MaxAge = 0 },
		"age-fractional":      func(c *Config) { c.Pages[0].MaxAge += time.Nanosecond },
		"age-unbounded":       func(c *Config) { c.Pages[0].MaxAge = MaxAge + time.Second },
		"two-generations":     func(c *Config) { c.Budget.Generations = 2 },
		"immediate-update":    func(c *Config) { c.Updates = "skip-waiting" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c := sampleConfig()
			change(&c)
			if _, err := Normalize(c); !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
}

func TestDefaultsAcceptMeasuredCoreWithoutEnlargingExplicitLimits(t *testing.T) {
	c := mustCatalog(t, sampleSpec(1000))
	if c.Policy().Config().Budget.AssetBytes < 12<<20 || c.Policy().Config().Budget.Generations != 3 {
		t.Fatal("unsafe default budget")
	}
	s := sampleSpec(1000)
	s.Config.Budget.AssetBytes = 8 << 20
	if _, err := NewCatalog(s); !errors.Is(err, ErrInvalid) {
		t.Fatal("explicit undersized asset budget was silently raised", err)
	}
	if c.Cost().Entries != len(c.Entries())+2 || c.Cost().Bytes < 8401692+MaxCatalogBytes+MaxCommitBytes {
		t.Fatal("records not charged")
	}
}

func TestCatalogRejectsIncompleteAndAliasedInventory(t *testing.T) {
	cases := map[string]func(*CatalogSpec){
		"missing-worker":   func(s *CatalogSpec) { s.Assets = append(s.Assets[:1], s.Assets[2:]...) },
		"missing-style":    func(s *CatalogSpec) { s.Assets = append(s.Assets[:4], s.Assets[5:]...) },
		"missing-fallback": func(s *CatalogSpec) { s.Documents = s.Documents[:1] },
		"duplicate":        func(s *CatalogSpec) { s.Assets = append(s.Assets, s.Assets[0]) },
		"foreign-origin": func(s *CatalogSpec) {
			s.Assets[0].URL = strings.Replace(s.Assets[0].URL, "example.test", "foreign.test", 1)
		},
		"peer-runtime": func(s *CatalogSpec) {
			s.Assets[0].URL = strings.Replace(s.Assets[0].URL, s.RuntimeID, strings.Repeat("d", 20), 1)
		},
		"mutable-public":     func(s *CatalogSpec) { s.Assets[4].URL = s.Config.Origin + s.Config.BasePath + "/assets/style.css" },
		"encoded-alias":      func(s *CatalogSpec) { s.Assets[0].URL = strings.Replace(s.Assets[0].URL, "app.wasm", "%61pp.wasm", 1) },
		"query":              func(s *CatalogSpec) { s.Assets[0].URL += "?v=1" },
		"worker-name":        func(s *CatalogSpec) { s.Assets[1].SHA256 = strings.Repeat("e", 64) },
		"wrong-mime":         func(s *CatalogSpec) { s.Assets[0].ContentType = "text/html" },
		"negative-size":      func(s *CatalogSpec) { s.Assets[0].DecodedBytes = -1 },
		"unbounded-size":     func(s *CatalogSpec) { s.Assets[0].DecodedBytes = 1 << 40 },
		"unknown-role":       func(s *CatalogSpec) { s.Assets[0].Kind = "api" },
		"implicit-gate":      func(s *CatalogSpec) { s.Admission = "" },
		"duplicate-document": func(s *CatalogSpec) { s.Documents[1].Path = "/" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s := sampleSpec(1000)
			change(&s)
			if c, err := NewCatalog(s); err == nil || c != nil {
				t.Fatal("invalid inventory admitted")
			}
		})
	}
}

func TestCatalogOwnsInputsAndIdentityBindsAllMaterialFields(t *testing.T) {
	s := sampleSpec(1000)
	c := mustCatalog(t, s)
	identity := c.Generation()
	s.Assets[0].SHA256 = strings.Repeat("e", 64)
	s.Documents[0].Path = "/private"
	s.Config.Pages[0].Path = "/private"
	copy := c.Spec()
	copy.Assets[0].URL = "https://foreign.test/"
	copy.Documents[0].SHA256 = "bad"
	copy.Config.Pages[0].Path = "/bad"
	entries := c.Entries()
	entries[0].URL = "changed"
	if c.Generation() != identity || c.Spec().Config.Pages[0].Path != "/" || c.Entries()[0].URL == "changed" {
		t.Fatal("mutable alias retained")
	}
	s = sampleSpec(1000)
	for i, j := 0, len(s.Assets)-1; i < j; i, j = i+1, j-1 {
		s.Assets[i], s.Assets[j] = s.Assets[j], s.Assets[i]
	}
	if mustCatalog(t, s).Generation() != identity {
		t.Fatal("enumeration order changed identity")
	}
	for _, change := range []func(*CatalogSpec){
		func(s *CatalogSpec) { s.SavedAtUnix++ },
		func(s *CatalogSpec) { s.Configuration = strings.Repeat("f", 64) },
		func(s *CatalogSpec) { s.Documents[0].SHA256 = strings.Repeat("f", 64) },
		func(s *CatalogSpec) { s.Config.Pages[0].MaxAge += time.Second },
		func(s *CatalogSpec) { s.Config.Budget.TotalBytes = (96 << 20) - 1024 },
	} {
		s := sampleSpec(1000)
		change(&s)
		if mustCatalog(t, s).Generation() == identity {
			t.Fatal("material field not bound")
		}
	}
}

func TestInventoryRequiresEveryDecodedBodyAndFreshnessNeverExtends(t *testing.T) {
	c := mustCatalog(t, sampleSpec(1000))
	good := observations(c)
	if err := c.VerifyInventory(good); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string][]Observation{"evicted": good[:len(good)-1], "extra": append(append([]Observation(nil), good...), good[0]), "marker-only": nil} {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(c.VerifyInventory(bad), ErrIncomplete) {
				t.Fatal("incomplete inventory accepted")
			}
		})
	}
	for field := 0; field < 4; field++ {
		bad := append([]Observation(nil), good...)
		switch field {
		case 0:
			bad[0] = bad[1]
		case 1:
			bad[0].SHA256 = strings.Repeat("e", 64)
		case 2:
			bad[0].DecodedBytes++
		case 3:
			bad[0].ContentType = "text/plain"
		}
		if c.VerifyInventory(bad) == nil {
			t.Fatal("corrupt inventory accepted")
		}
	}
	if c.Fresh("/", 999, 1000) || !c.Fresh("/", 1000, 1000) || !c.Fresh("/", 1000+6*3600-1, 1000) || c.Fresh("/", 1000+6*3600, 1000) || c.Fresh("/?private=1", 1000, 1000) || c.Fresh("/unknown", 1000, 1000) {
		t.Fatal("invalid freshness boundary")
	}
	expired := int64(1000 + 6*3600)
	if c.Fresh("/", expired-1, expired) {
		t.Fatal("expired snapshot revived after clock rollback")
	}
	if new(Catalog).Fresh("/", 1000, 1000) || (*Catalog)(nil).Fresh("/", 1000, 1000) || c.Fresh("/", 1000, 0) {
		t.Fatal("invalid clock/catalog state admitted")
	}
}

func TestZeroCatalogFailsClosed(t *testing.T) {
	for _, c := range []*Catalog{nil, new(Catalog)} {
		if err := c.VerifyInventory(nil); !errors.Is(err, ErrIncomplete) {
			t.Fatal("invalid empty inventory authorized", err)
		}
		if c.Fresh("/", 1000, 1000) {
			t.Fatal("invalid catalog authorized a saved document")
		}
	}
}
