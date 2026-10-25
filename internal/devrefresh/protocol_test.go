package devrefresh

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func sampleModule() Module {
	digest := strings.Repeat("a", 64)
	return Module{Name: "counter", URL: "/_nextgo/" + strings.Repeat("b", 20) + "/module-" + digest + ".wasm", Digest: digest, Size: 1024, Epoch: strings.Repeat("c", 64), StateSchema: "counter/v1", DOMSchema: "counter/v1"}
}

func sampleManifest() Manifest {
	return Manifest{Version: Version, ServingBuild: strings.Repeat("d", 20), ServingGeneration: strings.Repeat("e", 32), Compatibility: strings.Repeat("f", 64), ShimDigest: ShimDigest, ModuleABI: ModuleABI, Modules: []Module{sampleModule()}}
}

func jsonBytes(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestManifestRevisionAndCompatibility(t *testing.T) {
	baseline := sampleManifest()
	if _, err := ParseManifest(jsonBytes(t, baseline)); err != nil {
		t.Fatal(err)
	}
	next := baseline
	next.Revision = 1
	next.Modules = append([]Module(nil), baseline.Modules...)
	next.Modules[0].URL = "/mounted" + next.Modules[0].URL
	if err := next.Next(baseline); err != nil {
		t.Fatalf("URL-only generation repoint must not require runtime replacement: %v", err)
	}
	for _, test := range []struct {
		name string
		edit func(*Manifest)
		want error
	}{
		{"replay", func(m *Manifest) { m.Revision = 0 }, ErrRevision},
		{"gap", func(m *Manifest) { m.Revision, m.Previous = 2, 1 }, ErrRevision},
		{"serving build", func(m *Manifest) { m.ServingBuild = strings.Repeat("1", 20) }, ErrIncompatible},
		{"native generation", func(m *Manifest) { m.ServingGeneration = strings.Repeat("1", 32) }, ErrIncompatible},
		{"core graph", func(m *Manifest) { m.Compatibility = strings.Repeat("1", 64) }, ErrIncompatible},
		{"state schema", func(m *Manifest) { m.Modules[0].StateSchema = "counter/v2" }, ErrIncompatible},
		{"DOM schema", func(m *Manifest) { m.Modules[0].DOMSchema = "counter/v2" }, ErrIncompatible},
		{"component", func(m *Manifest) { m.Modules[0].Name = "another" }, ErrIncompatible},
		{"unsupported shim", func(m *Manifest) { m.ShimDigest = strings.Repeat("1", 64) }, ErrInvalid},
		{"public ABI", func(m *Manifest) { m.ModuleABI++ }, ErrInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := next
			candidate.Modules = append([]Module(nil), next.Modules...)
			test.edit(&candidate)
			if err := candidate.Next(baseline); !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
	last := baseline
	last.Revision, last.Previous = MaxRevision, MaxRevision-1
	if err := last.Next(last); !errors.Is(err, ErrRevision) {
		t.Fatalf("revision exhaustion: %v", err)
	}
}

func TestManifestModulesAreACompleteUnorderedTable(t *testing.T) {
	old := sampleManifest()
	second := sampleModule()
	second.Name = "nested/counter"
	old.Modules = append(old.Modules, second)
	next := old
	next.Revision = 1
	next.Modules = []Module{second, old.Modules[0]}
	if err := next.Next(old); err != nil {
		t.Fatalf("component ordering is not state identity: %v", err)
	}
	next.Modules = next.Modules[:1]
	if err := next.Next(old); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("partial module table accepted: %v", err)
	}
}

func TestStrictManifestParsing(t *testing.T) {
	valid := string(jsonBytes(t, sampleManifest()))
	for name, data := range map[string]string{
		"duplicate":       strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1),
		"decoded alias":   strings.Replace(valid, `"version":1`, `"version":1,"\u0076ersion":1`, 1),
		"case alias":      strings.Replace(valid, `"version":1`, `"Version":1`, 1),
		"extra alias":     strings.Replace(valid, `"version":1`, `"version":1,"Version":1`, 1),
		"nested case":     strings.Replace(valid, `"stateSchema":`, `"StateSchema":`, 1),
		"unknown":         strings.Replace(valid, `"version":1`, `"version":1,"state":"secret"`, 1),
		"missing":         strings.Replace(valid, `"previous":0,`, "", 1),
		"null revision":   strings.Replace(valid, `"revision":0`, `"revision":null`, 1),
		"trailing":        valid + " {}",
		"unsafe integer":  strings.Replace(valid, `"revision":0`, `"revision":9007199254740992`, 1),
		"empty modules":   strings.Replace(valid, `"modules":[`, `"modules":null,"unused":[`, 1),
		"oversized wire":  strings.Repeat(" ", MaxManifestBytes+1),
		"non UTF-8 token": strings.Replace(valid, "counter", "counter\xff", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(data)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted ambiguous or invalid wire payload: %v", err)
			}
		})
	}
}

func TestModuleResourceAdmission(t *testing.T) {
	valid := sampleModule()
	for _, prefix := range []string{"", "/mount", "/assets.v1/public~"} {
		m := valid
		m.URL = prefix + m.URL
		if err := m.Validate(); err != nil {
			t.Fatalf("canonical local resource rejected: %v", err)
		}
	}
	for name, edit := range map[string]func(*Module){
		"external":          func(m *Module) { m.URL = "https://other.test" + m.URL },
		"authority":         func(m *Module) { m.URL = "/" + m.URL },
		"encoded separator": func(m *Module) { m.URL = "/mount%2f" + m.URL },
		"dot segment":       func(m *Module) { m.URL = "/a/../" + m.URL[1:] },
		"query":             func(m *Module) { m.URL += "?token=private" },
		"fragment":          func(m *Module) { m.URL += "#part" },
		"backslash":         func(m *Module) { m.URL = "/a\\b" + m.URL },
		"wrong digest":      func(m *Module) { m.Digest = strings.Repeat("1", 64) },
		"wrong build":       func(m *Module) { m.URL = strings.Replace(m.URL, strings.Repeat("b", 20), "development", 1) },
		"zero bytes":        func(m *Module) { m.Size = 0 },
		"oversized":         func(m *Module) { m.Size = MaxModuleBytes + 1 },
		"unknown epoch":     func(m *Module) { m.Epoch = "old" },
		"schema control":    func(m *Module) { m.StateSchema = "counter\nv1" },
		"component path":    func(m *Module) { m.Name = "nested/../counter" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			edit(&candidate)
			if candidate.Validate() == nil {
				t.Fatal("unsafe module descriptor accepted")
			}
		})
	}
	m := sampleManifest()
	m.Modules = make([]Module, MaxModules+1)
	for i := range m.Modules {
		m.Modules[i] = valid
		m.Modules[i].Name = fmt.Sprintf("module%d", i)
	}
	if m.Validate() == nil {
		t.Fatal("module-count cap not enforced")
	}
	m.Modules = []Module{valid, valid}
	if m.Validate() == nil {
		t.Fatal("duplicate component admitted")
	}
}

func TestPublicTokens(t *testing.T) {
	for _, token := range []string{"count", "Counter_2", "counter/v1", "public:local.value-1", strings.Repeat("a", MaxTokenBytes)} {
		if !ValidToken(token) {
			t.Errorf("valid token rejected")
		}
	}
	for _, token := range []string{"", "_private", "/first", "state with spaces", "value\x00", "é", strings.Repeat("a", MaxTokenBytes+1)} {
		if ValidToken(token) {
			t.Errorf("invalid token admitted")
		}
	}
}
