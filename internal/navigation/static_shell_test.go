package navigation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

func staticShellFixture(t *testing.T, deployment paths.Config) (StaticShellDescriptor, []byte) {
	t.Helper()
	body := []byte(`{"version":3,"url":"/workspace","html":"public seed"}`)
	sum := sha256.Sum256(body)
	d := StaticShellDescriptor{Version: 1, Build: strings.Repeat("a", 20), Configuration: strings.Repeat("b", 64), Pattern: "/workspace/*rest?", Component: "workspace", SHA256: hex.EncodeToString(sum[:]), Bytes: len(body)}
	var err error
	d.SeedURL, err = deployment.URL("/workspace")
	if err != nil {
		t.Fatal(err)
	}
	d.EnvelopeURL, err = StaticShellEnvelopeURL(deployment, d.Build, d.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	return d, body
}

func TestStaticShellDescriptorStrictRoundTrip(t *testing.T) {
	for _, deployment := range []paths.Config{{}, {BasePath: "/docs", TrailingSlash: true, AssetPrefix: "https://cdn.example/static"}} {
		d, envelope := staticShellFixture(t, deployment)
		encoded, err := EncodeStaticShellDescriptor(d, deployment, d.Build, d.Configuration)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(encoded, &fields) != nil || len(fields) != 9 {
			t.Fatal("descriptor must have exactly nine fields", string(encoded))
		}
		decoded, err := DecodeStaticShellDescriptor(encoded, deployment, d.Build, d.Configuration)
		if err != nil || decoded != d || decoded.CheckEnvelope(envelope) != nil {
			t.Fatal(decoded, err)
		}
		if strings.Contains(decoded.EnvelopeURL, "cdn.example") || !strings.HasPrefix(decoded.EnvelopeURL, deployment.BasePath+"/_nextgo/") {
			t.Fatal("shell envelope must remain application-mounted", decoded.EnvelopeURL)
		}
	}
}

func TestStaticShellDescriptorRejectsJSONAmbiguity(t *testing.T) {
	d, _ := staticShellFixture(t, paths.Config{})
	encoded, err := EncodeStaticShellDescriptor(d, paths.Config{}, d.Build, d.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	base := string(encoded)
	boundary := append(append([]byte(nil), encoded...), bytes.Repeat([]byte(" "), MaxStaticShellDescriptorBytes-len(encoded))...)
	if _, err := DecodeStaticShellDescriptor(boundary, paths.Config{}, d.Build, d.Configuration); err != nil {
		t.Fatal("exact raw JSON cap rejected", err)
	}
	inputs := map[string][]byte{
		"empty":             nil,
		"null object":       []byte("null"),
		"array":             []byte("[]"),
		"duplicate":         []byte(strings.Replace(base, `"version":1`, `"version":1,"version":1`, 1)),
		"escaped duplicate": []byte(strings.Replace(base, `"version":1`, `"version":1,"\u0076ersion":1`, 1)),
		"case alias":        []byte(strings.Replace(base, `"build":`, `"Build":`, 1)),
		"unknown":           []byte(strings.TrimSuffix(base, "}") + `,"extra":true}`),
		"missing":           []byte(strings.Replace(base, `"version":1,`, "", 1)),
		"trailing":          []byte(base + "{}"),
		"future version":    []byte(strings.Replace(base, `"version":1`, `"version":2`, 1)),
		"fraction version":  []byte(strings.Replace(base, `"version":1`, `"version":1.0`, 1)),
		"exponent version":  []byte(strings.Replace(base, `"version":1`, `"version":1e0`, 1)),
		"string version":    []byte(strings.Replace(base, `"version":1`, `"version":"1"`, 1)),
		"string byte count": []byte(strings.Replace(base, `"bytes":`+stringInt(d.Bytes), `"bytes":"`+stringInt(d.Bytes)+`"`, 1)),
		"negative bytes":    []byte(strings.Replace(base, `"bytes":`+stringInt(d.Bytes), `"bytes":-1`, 1)),
		"raw JSON cap":      append(boundary, ' '),
		"oversize input":    bytes.Repeat([]byte(" "), MaxStaticShellDescriptorBytes+1),
		"invalid UTF8":      bytes.Replace(encoded, []byte("workspace"), []byte{0xff}, 1),
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	for key := range raw {
		for _, replacement := range []string{"null", "true", "[]", "{}"} {
			copy := make(map[string]json.RawMessage, len(raw))
			for k, v := range raw {
				copy[k] = v
			}
			copy[key] = json.RawMessage(replacement)
			input, err := json.Marshal(copy)
			if err != nil {
				t.Fatal(err)
			}
			inputs[key+"="+replacement] = input
		}
	}
	for name, input := range inputs {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeStaticShellDescriptor(input, paths.Config{}, d.Build, d.Configuration); err == nil {
				t.Fatal("invalid descriptor admitted", string(input))
			}
		})
	}
}

func stringInt(n int) string {
	value, _ := json.Marshal(n)
	return string(value)
}

func TestStaticShellDescriptorDerivedIdentityAndBounds(t *testing.T) {
	deployment := paths.Config{BasePath: "/docs", TrailingSlash: true}
	base, _ := staticShellFixture(t, deployment)
	changes := map[string]func(*StaticShellDescriptor){
		"wrong build": func(d *StaticShellDescriptor) { d.Build = strings.Repeat("c", 20) },
		"manual build ID unsupported in descriptor1": func(d *StaticShellDescriptor) { d.Build = "development" },
		"wrong configuration":                        func(d *StaticShellDescriptor) { d.Configuration = strings.Repeat("c", 64) },
		"uppercase configuration":                    func(d *StaticShellDescriptor) { d.Configuration = strings.Repeat("B", 64) },
		"uppercase hash":                             func(d *StaticShellDescriptor) { d.SHA256 = strings.Repeat("A", 64) },
		"empty envelope":                             func(d *StaticShellDescriptor) { d.Bytes = 0 },
		"oversize envelope":                          func(d *StaticShellDescriptor) { d.Bytes = MaxStaticShellEnvelopeBytes + 1 },
		"wrong seed slash":                           func(d *StaticShellDescriptor) { d.SeedURL = "/docs/workspace" },
		"seed query":                                 func(d *StaticShellDescriptor) { d.SeedURL += "?x=1" },
		"seed fragment":                              func(d *StaticShellDescriptor) { d.SeedURL += "#x" },
		"seed escaped alias":                         func(d *StaticShellDescriptor) { d.SeedURL = "/docs/%77orkspace/" },
		"envelope authority":                         func(d *StaticShellDescriptor) { d.EnvelopeURL = "https://example.test" + d.EnvelopeURL },
		"envelope alias": func(d *StaticShellDescriptor) {
			d.EnvelopeURL = strings.Replace(d.EnvelopeURL, "/_nextgo/", "/%5fnextgo/", 1)
		},
		"wrong envelope digest URL": func(d *StaticShellDescriptor) {
			d.EnvelopeURL = strings.Replace(d.EnvelopeURL, d.SHA256, strings.Repeat("c", 64), 1)
		},
		"envelope extra slash": func(d *StaticShellDescriptor) { d.EnvelopeURL += "/" },
		"component controls":   func(d *StaticShellDescriptor) { d.Component = "workspace\n" },
		"component bound":      func(d *StaticShellDescriptor) { d.Component = strings.Repeat("x", 257) },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			d := base
			change(&d)
			if _, err := EncodeStaticShellDescriptor(d, deployment, base.Build, base.Configuration); err == nil {
				t.Fatal("invalid identity encoded")
			}
		})
	}
	if base.Validate(deployment, base.Build, strings.Repeat("d", 64)) == nil || base.Validate(paths.Config{BasePath: "/other"}, base.Build, base.Configuration) == nil {
		t.Fatal("document identity mismatch admitted")
	}
	// Each field is individually bounded, but the repeated prefix in the seed
	// URL can still exceed the aggregate JSON cap.
	d := base
	d.Pattern = "/" + strings.Repeat("x", 8100) + "/*rest?"
	seed, err := ValidateStaticShellDeclaration(d.Pattern, d.Component)
	if err != nil {
		t.Fatal(err)
	}
	d.SeedURL, _ = deployment.URL(seed)
	if _, err := EncodeStaticShellDescriptor(d, deployment, d.Build, d.Configuration); err == nil {
		t.Fatal("aggregate serialized descriptor cap not enforced")
	}
}

func TestStaticShellDeclarationLiteralOptionalSubtree(t *testing.T) {
	for _, pattern := range []string{"/*rest?", "/workspace", "/workspace/*rest", "/:tenant/*rest?", "/workspace/:id/*rest?", "/workspace/*rest?/child", "/workspace//child/*rest?", "/workspace/../*rest?", "/api/*rest?", "/_nextgo/*rest?", "/assets/images/*rest?", "/.well-known/*rest?", "/workspace/*rest??", "/workspace/*9bad?"} {
		if _, err := ValidateStaticShellDeclaration(pattern, "workspace"); err == nil {
			t.Fatal("invalid declaration admitted", pattern)
		}
	}
	for pattern, want := range map[string]string{"/workspace/*rest?": "/workspace", "/work space/東京/*rest?": "/work%20space/%E6%9D%B1%E4%BA%AC", "/literal%20/*rest?": "/literal%2520"} {
		if seed, err := ValidateStaticShellDeclaration(pattern, "workspace"); err != nil || seed != want {
			t.Fatal(pattern, seed, want, err)
		}
	}
}

func TestStaticShellAdmissionUsesLiteralMountPrefixAndFirstPage(t *testing.T) {
	deployment := paths.Config{BasePath: "/docs", TrailingSlash: true}
	d, _ := staticShellFixture(t, deployment)
	manifest := Manifest{Routes: []Route{{Pattern: d.Pattern}}}
	for path, tail := range map[string]string{"/docs/workspace": "", "/docs/workspace/": "", "/docs/workspace/project/%E6%9D%B1%E4%BA%AC/": "project/東京", "/docs/workspace/%252f": "%2f"} {
		params, ok := d.MatchTarget(path, deployment, manifest)
		if !ok || params["rest"] != tail {
			t.Fatal(path, params, ok)
		}
	}
	for _, path := range []string{"/workspace/a", "/docsevil/workspace/a", "/%64ocs/workspace/a", "/docs/%77orkspace/a", "/docs/Workspace/a", "/docs/workspace//", "/docs/workspace//a", "/docs/workspace/%2f", "/docs/workspace/%5c", "/docs/workspace/%2e", "/docs/workspace/%2e%2e", "/docs/workspace/%00", "/docs/workspace/%7f", "/docs/workspace/%ff", "/docs/workspace/%", "/docs/workspace/a?x=1", "/docs/workspace/a#x"} {
		if params, ok := d.MatchTarget(path, deployment, manifest); ok || params != nil {
			t.Fatal("invalid target admitted", path, params)
		}
	}
	for name, conflicting := range map[string]Manifest{
		"literal page wins": {Routes: []Route{{Pattern: "/workspace/special"}, {Pattern: d.Pattern}}},
		"endpoint wins":     {Routes: []Route{{Pattern: "/workspace/special", Endpoint: true}, {Pattern: d.Pattern}}},
		"document bypass":   {Routes: manifest.Routes, Documents: []string{"/workspace/*rest?"}},
		"handler bypass":    {Routes: manifest.Routes, Handlers: []string{"/workspace/"}},
		"undeclared page":   {Routes: []Route{{Pattern: "/other/*rest?"}}},
		"catchall alias":    {Routes: []Route{{Pattern: "/workspace/*different?"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := d.MatchTarget("/docs/workspace/special", deployment, conflicting); ok {
				t.Fatal("manifest ownership bypass")
			}
		})
	}
}

func TestStaticShellEnvelopeChecksExactImmutableBytes(t *testing.T) {
	d, body := staticShellFixture(t, paths.Config{})
	if d.CheckEnvelope(body) != nil {
		t.Fatal("valid envelope refused")
	}
	changed := append([]byte(nil), body...)
	changed[len(changed)-1] ^= 1
	if d.CheckEnvelope(changed) == nil || d.CheckEnvelope(append(body, ' ')) == nil || d.CheckEnvelope(nil) == nil {
		t.Fatal("mutated envelope admitted")
	}
	if !bytes.Equal(body, []byte(`{"version":3,"url":"/workspace","html":"public seed"}`)) {
		t.Fatal("immutable input changed")
	}
}
