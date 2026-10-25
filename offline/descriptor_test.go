package offline

import (
	"strings"
	"testing"
	"time"
)

func sampleClientDescriptor() ClientDescriptor {
	s := sampleSpec(1000)
	root := s.Config.Origin + s.Config.BasePath
	return ClientDescriptor{Version: Version, Scope: scopeURL(s.Config.Origin, s.Config.BasePath), WorkerURL: root + WorkerScriptPath, CatalogURL: root + CatalogPath, OwnerPolicyID: s.Config.ID, RuntimeID: s.RuntimeID, ConfigurationID: s.Configuration}
}

func TestClientDescriptorBindsExactTrustedGeometryAndIdentity(t *testing.T) {
	p, err := Normalize(sampleConfig())
	if err != nil {
		t.Fatal(err)
	}
	d := sampleClientDescriptor()
	if _, err := NormalizeClientDescriptor(d, p, d.RuntimeID, d.ConfigurationID); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*ClientDescriptor){
		"version":           func(d *ClientDescriptor) { d.Version++ },
		"scope-child":       func(d *ClientDescriptor) { d.Scope += "/child/" },
		"scope-added-slash": func(d *ClientDescriptor) { d.Scope += "/" },
		"foreign-worker": func(d *ClientDescriptor) {
			d.WorkerURL = strings.Replace(d.WorkerURL, "example.test", "foreign.test", 1)
		},
		"encoded-worker":     func(d *ClientDescriptor) { d.WorkerURL = strings.Replace(d.WorkerURL, "offline", "%6fffline", 1) },
		"worker-query":       func(d *ClientDescriptor) { d.WorkerURL += "?v=1" },
		"old-worker-path":    func(d *ClientDescriptor) { d.WorkerURL = p.config.Origin + p.config.BasePath + "/nextgo-sw.js" },
		"catalog-json-alias": func(d *ClientDescriptor) { d.CatalogURL += ".json" },
		"owner":              func(d *ClientDescriptor) { d.OwnerPolicyID = "other-policy" },
		"runtime":            func(d *ClientDescriptor) { d.RuntimeID = strings.Repeat("d", 20) },
		"configuration":      func(d *ClientDescriptor) { d.ConfigurationID = strings.Repeat("d", 64) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			bad := d
			change(&bad)
			if _, err := NormalizeClientDescriptor(bad, p, d.RuntimeID, d.ConfigurationID); err == nil {
				t.Fatal("foreign descriptor admitted")
			}
		})
	}
	if ValidateClientDescriptor(d, p.config.Origin, p.config.BasePath, d.RuntimeID, d.ConfigurationID) != nil {
		t.Fatal("document validation rejected trusted geometry")
	}
	if _, err := NormalizeClientDescriptor(d, new(Policy), d.RuntimeID, d.ConfigurationID); err == nil {
		t.Fatal("zero policy accepted")
	}
}

func TestStorageOwnershipSurvivesPolicyChangesWithoutCrossingOwners(t *testing.T) {
	p, err := Normalize(sampleConfig())
	if err != nil {
		t.Fatal(err)
	}
	changed := sampleConfig()
	changed.Budget.TotalBytes = 95 << 20
	changed.Pages[0].MaxAge += time.Second
	q, err := Normalize(changed)
	if err != nil {
		t.Fatal(err)
	}
	if p.StorageID() != q.StorageID() || p.StorageNamespace() != StorageNamespacePrefix+p.StorageID() {
		t.Fatal("policy change abandoned owned generations")
	}
	for _, change := range []func(*Config){func(c *Config) { c.ID = "different" }, func(c *Config) { c.Origin = "https://different.test" }, func(c *Config) { c.BasePath = "/different" }} {
		config := sampleConfig()
		change(&config)
		other, err := Normalize(config)
		if err != nil {
			t.Fatal(err)
		}
		if other.StorageID() == p.StorageID() {
			t.Fatal("foreign owner shared storage identity")
		}
	}
	if new(Policy).StorageID() != "" || (*Policy)(nil).StorageNamespace() != "" {
		t.Fatal("invalid policy acquired storage namespace")
	}
}

func TestCanonicalDocumentAndScopeIncludeMountedApplicationRoot(t *testing.T) {
	for _, base := range []string{"", "/guide"} {
		config := sampleConfig()
		config.BasePath = base
		p, err := Normalize(config)
		if err != nil {
			t.Fatal(err)
		}
		want := config.Origin + base
		if base == "" {
			want += "/"
		}
		root, err := p.DocumentURL("/")
		if err != nil || root != want || p.ScopeURL() != want {
			t.Fatal("canonical root outside worker scope", root, p.ScopeURL(), err)
		}
		fallback, err := p.DocumentURL("/offline")
		if err != nil || fallback != config.Origin+base+"/offline" {
			t.Fatal(fallback, err)
		}
		spec := sampleSpec(1000)
		spec.Config = config
		for i := range spec.Assets {
			spec.Assets[i].URL = strings.Replace(spec.Assets[i].URL, "https://example.test/guide/", "https://example.test"+base+"/", 1)
		}
		catalog := mustCatalog(t, spec)
		found := false
		for _, entry := range catalog.Entries() {
			if entry.Kind == "document" {
				found = true
				if entry.URL != want {
					t.Fatal("catalog root is not canonical", entry.URL, want)
				}
			}
		}
		if !found {
			t.Fatal("root document missing")
		}
		if _, err := p.DocumentURL("/unknown"); err == nil {
			t.Fatal("undeclared document acquired URL permission")
		}
	}
}

func TestWorkerDescriptorBindsExactCatalogBytesAndStableOwnerNamespace(t *testing.T) {
	p, err := Normalize(sampleConfig())
	if err != nil {
		t.Fatal(err)
	}
	client := sampleClientDescriptor()
	// Content interpretation is deliberately separate. The strict native/worker
	// codec and NewCatalog must validate this payload before it can be used.
	d := WorkerDescriptor{Version: Version, Client: client, Generation: strings.Repeat("d", 64), CatalogJSON: "{}", CatalogSHA256: digest("{}"), Namespace: p.StorageNamespace()}
	if _, err := NormalizeWorkerDescriptor(d, p, client.RuntimeID, client.ConfigurationID); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*WorkerDescriptor){
		"version":          func(d *WorkerDescriptor) { d.Version++ },
		"generation":       func(d *WorkerDescriptor) { d.Generation = "unbound" },
		"catalog-mismatch": func(d *WorkerDescriptor) { d.CatalogJSON += " " },
		"catalog-sha":      func(d *WorkerDescriptor) { d.CatalogSHA256 = strings.Repeat("0", 64) },
		"catalog-cap": func(d *WorkerDescriptor) {
			d.CatalogJSON = strings.Repeat(" ", MaxCatalogBytes+1)
			d.CatalogSHA256 = digest(d.CatalogJSON)
		},
		"namespace": func(d *WorkerDescriptor) { d.Namespace = StorageNamespacePrefix + strings.Repeat("f", 64) },
		"client":    func(d *WorkerDescriptor) { d.Client.OwnerPolicyID = "other" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			bad := d
			change(&bad)
			if _, err := NormalizeWorkerDescriptor(bad, p, client.RuntimeID, client.ConfigurationID); err == nil {
				t.Fatal("unbound worker descriptor admitted")
			}
		})
	}
}
