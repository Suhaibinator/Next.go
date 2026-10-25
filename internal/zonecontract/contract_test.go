package zonecontract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/actionwire"
	"github.com/Suhaibinator/Next.go/internal/navigation"
)

const runtimeID = "0123456789abcdefabcd"

func testSpec(id, base, release string) ReleaseSpec {
	return ReleaseSpec{
		Version: Version, Zone: Binding{ID: id, Host: "example.test", BasePath: base},
		ReleaseID: release, RuntimeID: runtimeID, PublicOrigin: "https://example.test",
		AssetPrefix:  base + "/_nextgo/releases/" + release,
		AssetsSHA256: strings.Repeat("a", 64), TopologySHA256: strings.Repeat("b", 64),
		NavigationProtocol: navigation.ProtocolVersion, ActionProtocol: actionwire.Protocol,
	}
}

func testIngress() IngressManifest {
	root := testSpec("root", "", "A1")
	root.DocumentPaths = []string{"/blog"}
	blog := testSpec("blog", "/blog", "B1")
	return IngressManifest{Version: Version, TopologySHA256: root.TopologySHA256, Zones: []IngressZone{
		{Binding: root.Zone, ActiveRelease: root.ReleaseID, Releases: []IngressRelease{{Artifact: ReleaseManifest{Release: root, BinarySHA256: strings.Repeat("c", 64)}, Origin: "http://root.internal:8080"}}},
		{Binding: blog.Zone, ActiveRelease: blog.ReleaseID, Releases: []IngressRelease{{Artifact: ReleaseManifest{Release: blog, BinarySHA256: strings.Repeat("d", 64)}, Origin: "https://blog.internal:8443"}}},
	}}
}

func TestNormalizeAndParseOwnCanonicalRoutingSnapshot(t *testing.T) {
	input := testIngress()
	input.Zones[0].Binding.Host = "EXAMPLE.TEST."
	input.Zones[0].Releases[0].Artifact.Release.Zone.Host = "EXAMPLE.TEST."
	input.Zones[0].Releases[0].Artifact.Release.PublicOrigin = "HTTPS://EXAMPLE.TEST."
	input.Zones[0].Releases[0].Origin = "HTTP://ROOT.INTERNAL.:8080"
	input.Zones[0].Releases[0].Artifact.Release.DocumentPaths = []string{"/support", "/blog", "/blog"}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := ParseIngressManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	root := actual.Zones[1]
	if root.Binding.ID != "root" || root.Binding.Host != "example.test" || root.Releases[0].Origin != "http://root.internal:8080" || root.Releases[0].Artifact.Release.PublicOrigin != "https://example.test" || !reflect.DeepEqual(root.Releases[0].Artifact.Release.DocumentPaths, []string{"/blog", "/support"}) {
		t.Fatal("routing snapshot not canonical", actual)
	}
	if input.Zones[0].Binding.Host != "EXAMPLE.TEST." || len(input.Zones[0].Releases[0].Artifact.Release.DocumentPaths) != 3 {
		t.Fatal("normalization changed caller input")
	}
	copy := actual.Clone()
	copy.Zones[1].Releases[0].Artifact.Release.DocumentPaths[0] = "/changed"
	copy.Zones[0].Releases[0].Origin = "http://changed.test"
	copy.Zones[0].Binding.ID = "changed"
	if root.Releases[0].Artifact.Release.DocumentPaths[0] != "/blog" || actual.Zones[0].Releases[0].Origin != "https://blog.internal:8443" || actual.Zones[0].Binding.ID != "blog" {
		t.Fatal("clone shared nested mutable storage")
	}
	encoded, err := json.Marshal(actual.Zones[0].Releases[0].Artifact)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseReleaseManifest(encoded)
	if err != nil || !reflect.DeepEqual(parsed, actual.Zones[0].Releases[0].Artifact) {
		t.Fatal("standalone release manifest did not roundtrip", parsed, err)
	}
}

func TestOriginAndReleasePrefixConfinement(t *testing.T) {
	for input, want := range map[string]string{"HTTPS://EXAMPLE.TEST.": "https://example.test", "http://[::1]:8080": "http://[::1]:8080", "https://example.test:443": "https://example.test:443"} {
		if actual, err := CanonicalOrigin(input); err != nil || actual != want {
			t.Fatal(input, actual, err)
		}
	}
	for _, input := range []string{"", "https://example.test/", "https://example.test/path", "https://user:pass@example.test", "https://example.test?", "https://example.test#", "//example.test", "ftp://example.test", "https://*.example.test", "https://example.test:", "https://example.test\\evil", "https://example.test%2f.evil", "https://example.test\n", "http://[::1]:0"} {
		if _, err := CanonicalOrigin(input); err == nil {
			t.Errorf("accepted non-origin %q", input)
		}
	}
	if actual, err := ReleasePrefix("/Blog", "A1"); err != nil || actual != "/Blog/_nextgo/releases/A1" {
		t.Fatal(actual, err)
	}
	for _, input := range [][2]string{{"/", "A1"}, {"/blog/", "A1"}, {"/%62log", "A1"}, {"/blog", "../A1"}, {"/blog", ""}, {"/blog", "."}, {"/blog", strings.Repeat("a", 65)}} {
		if _, err := ReleasePrefix(input[0], input[1]); err == nil {
			t.Errorf("accepted escaping release prefix %q", input)
		}
	}
}

func TestManifestIdentityAndOwnershipFailures(t *testing.T) {
	mutations := map[string]func(*IngressManifest){
		"version":           func(v *IngressManifest) { v.Version++ },
		"topology":          func(v *IngressManifest) { v.TopologySHA256 = strings.Repeat("A", 64) },
		"empty-zones":       func(v *IngressManifest) { v.Zones = nil },
		"too-many-zones":    func(v *IngressManifest) { v.Zones = make([]IngressZone, MaxZones+1) },
		"duplicate-binding": func(v *IngressManifest) { v.Zones[1].Binding = v.Zones[0].Binding },
		"case-colliding-id": func(v *IngressManifest) { v.Zones[1].Binding.ID = "ROOT" },
		"reserved-child":    func(v *IngressManifest) { v.Zones[1].Binding.BasePath = "/_nextgo/releases/A1" },
		"unknown-active":    func(v *IngressManifest) { v.Zones[0].ActiveRelease = "A2" },
		"case-active":       func(v *IngressManifest) { v.Zones[0].ActiveRelease = "a1" },
		"no-releases":       func(v *IngressManifest) { v.Zones[0].Releases = nil },
		"too-many-releases": func(v *IngressManifest) { v.Zones[0].Releases = make([]IngressRelease, MaxReleasesPerZone+1) },
		"duplicate-release": func(v *IngressManifest) { v.Zones[0].Releases = append(v.Zones[0].Releases, v.Zones[0].Releases[0]) },
		"origin-path":       func(v *IngressManifest) { v.Zones[0].Releases[0].Origin += "/blog" },
		"binding-mismatch":  func(v *IngressManifest) { v.Zones[0].Releases[0].Artifact.Release.Zone.ID = "other" },
		"digest-mismatch": func(v *IngressManifest) {
			v.Zones[0].Releases[0].Artifact.Release.TopologySHA256 = strings.Repeat("e", 64)
		},
		"no-nested-boundary": func(v *IngressManifest) { v.Zones[0].Releases[0].Artifact.Release.DocumentPaths = nil },
		"cross-scheme":       func(v *IngressManifest) { v.Zones[1].Releases[0].Artifact.Release.PublicOrigin = "http://example.test" },
		"no-binary-identity": func(v *IngressManifest) { v.Zones[0].Releases[0].Artifact.BinarySHA256 = "" },
		"no-assets-identity": func(v *IngressManifest) { v.Zones[0].Releases[0].Artifact.Release.AssetsSHA256 = "" },
		"bad-assets-identity": func(v *IngressManifest) {
			v.Zones[0].Releases[0].Artifact.Release.AssetsSHA256 = strings.Repeat("g", 64)
		},
		"invalid-runtime":       func(v *IngressManifest) { v.Zones[0].Releases[0].Artifact.Release.RuntimeID = "other" },
		"wrong-navigation":      func(v *IngressManifest) { v.Zones[0].Releases[0].Artifact.Release.NavigationProtocol++ },
		"wrong-action":          func(v *IngressManifest) { v.Zones[0].Releases[0].Artifact.Release.ActionProtocol = "2" },
		"wrong-release-version": func(v *IngressManifest) { v.Zones[0].Releases[0].Artifact.Release.Version++ },
		"prefix-alias": func(v *IngressManifest) {
			v.Zones[0].Releases[0].Artifact.Release.AssetPrefix = "/_nextgo/releases/%41"
		},
		"public-authority": func(v *IngressManifest) { v.Zones[0].Releases[0].Artifact.Release.PublicOrigin = "https://other.test" },
		"document-root":    func(v *IngressManifest) { v.Zones[0].Releases[0].Artifact.Release.DocumentPaths = []string{""} },
		"document-count": func(v *IngressManifest) {
			v.Zones[0].Releases[0].Artifact.Release.DocumentPaths = make([]string, MaxDocumentPaths+1)
		},
		"document-alias": func(v *IngressManifest) { v.Zones[0].Releases[0].Artifact.Release.DocumentPaths = []string{"/%62log"} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			value := testIngress()
			mutate(&value)
			if _, err := NormalizeIngressManifest(value); err == nil {
				t.Fatal("accepted invalid release routing")
			}
		})
	}
}

func TestParseRejectsAmbiguousOrUnboundedWireInput(t *testing.T) {
	data, err := json.Marshal(testIngress())
	if err != nil {
		t.Fatal(err)
	}
	valid := string(data)
	cases := []string{
		"", "null", "[]", valid + " {}", valid + " trailing",
		strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(valid, `"version":1`, `"version":1,"Version":1`, 1),
		strings.Replace(valid, `"version":1`, `"Version":1`, 1),
		strings.Replace(valid, `"version":1`, `"version":1,"future":true`, 1),
		strings.Replace(valid, `"basePath":""`, `"basePath":null`, 1),
		strings.Replace(valid, `,"basePath":""`, ``, 1),
		strings.Replace(valid, `"documentPaths":["/blog"]`, `"documentPaths":null`, 1),
		strings.Replace(valid, `"zone":{`, `"zone":{"unknown":"value",`, 1),
		string([]byte{0xff}), strings.Repeat(" ", MaxManifestBytes+1),
	}
	for i, value := range cases {
		if _, err := ParseIngressManifest([]byte(value)); err == nil {
			t.Errorf("accepted ambiguous input case %d", i)
		}
	}
}

func TestTopologyDigestBindsNativeTenantAndIgnoresInputOrder(t *testing.T) {
	values := []TopologyZone{{Binding: Binding{ID: "root", Host: "EXAMPLE.TEST.", BasePath: ""}, Tenant: "team-a"}, {Binding: Binding{ID: "blog", Host: "example.test", BasePath: "/blog"}}}
	first, err := TopologyDigest(values)
	if err != nil {
		t.Fatal(err)
	}
	other := []TopologyZone{values[1].Clone(), values[0].Clone()}
	other[1].Binding.Host = "example.test"
	if got, err := TopologyDigest(other); err != nil || got != first {
		t.Fatal("order or canonical host alias changed ownership digest", got, err)
	}
	other[1].Tenant = "team-b"
	if got, err := TopologyDigest(other); err != nil || got == first {
		t.Fatal("tenant reassignment did not change topology", got, err)
	}
	bindings := []Binding{values[0].Binding, values[1].Binding, {ID: "deep", Host: "example.test", BasePath: "/blog/deep"}, {ID: "remote", Host: "other.test", BasePath: "/other"}}
	paths, err := NestedDocumentPaths(values[0].Binding, bindings)
	if err != nil || !reflect.DeepEqual(paths, []string{"/blog", "/blog/deep"}) {
		t.Fatal(paths, err)
	}
	if _, err := NestedDocumentPaths(Binding{ID: "absent", Host: "example.test"}, bindings); err == nil {
		t.Fatal("selected absent zone accepted")
	}
	for _, prefix := range []string{"/api/x", "/assets/x", "/_nextgo/x", "/robots.txt/x", "/manifest.webmanifest/x"} {
		if _, err := TopologyDigest([]TopologyZone{values[0], {Binding: Binding{ID: "child", Host: "example.test", BasePath: prefix}}}); err == nil {
			t.Errorf("reserved parent ownership %s accepted", prefix)
		}
	}
}

func TestPrivateIdentityRequiresSingletonsAndStripsAllAliases(t *testing.T) {
	expected := testSpec("root", "", "A1").Expectation()
	header := http.Header{
		"x-nextgo-zone-id": {"attacker"}, HeaderZoneID: {"second"},
		"X-Nextgo-Zone-Future": {"future"}, "Trailer:" + HeaderReleaseID: {"late"},
		"Trailer": {HeaderZoneID + ", X-Public-Trailer"}, "Authorization": {"Bearer application"},
	}
	SetExpectation(header, expected)
	if err := CheckExpectation(header, expected); err != nil {
		t.Fatal(err, header)
	}
	if header.Get("Authorization") != "Bearer application" || header.Get("Trailer") != "X-Public-Trailer" {
		t.Fatal("unrelated application headers changed", header)
	}
	mutations := map[string]func(http.Header){
		"missing":        func(h http.Header) { h.Del(HeaderTopology) },
		"duplicate":      func(h http.Header) { h.Add(HeaderZoneID, expected.ZoneID) },
		"case-alias":     func(h http.Header) { h["x-nextgo-zone-id"] = []string{expected.ZoneID} },
		"nil-alias":      func(h http.Header) { h["x-nextgo-zone-id"] = nil },
		"wrong-release":  func(h http.Header) { h.Set(HeaderReleaseID, "A2") },
		"wrong-protocol": func(h http.Header) { h.Set(HeaderProtocol, "2") },
		"joined":         func(h http.Header) { h.Set(HeaderZoneID, expected.ZoneID+", "+expected.ZoneID) },
		"unknown":        func(h http.Header) { h.Set("X-Nextgo-Zone-Future", "1") },
		"trailer":        func(h http.Header) { h["Trailer:"+HeaderReleaseID] = []string{expected.ReleaseID} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			copy := header.Clone()
			mutate(copy)
			if err := CheckExpectation(copy, expected); err == nil {
				t.Fatal("ambiguous private identity accepted")
			}
		})
	}
	StripExpectation(header)
	if err := CheckExpectation(header, expected); err == nil || len(header) != 2 {
		t.Fatal("private metadata survived strip", header)
	}
	SetExpectation(nil, expected)
	if err := CheckExpectation(nil, expected); err == nil {
		t.Fatal("nil header established identity")
	}
	if err := ValidateExpectation(Expectation{}); err == nil {
		t.Fatal("invalid expectation accepted")
	}
}

func TestStructuredManifestCannotBypassWireByteBudgets(t *testing.T) {
	spec := testSpec("root", "", "A1")
	for i := 0; i < 9; i++ {
		spec.DocumentPaths = append(spec.DocumentPaths, "/"+strings.Repeat("x", 8190)+fmt.Sprint(i))
	}
	if _, err := NormalizeReleaseSpec(spec); err == nil {
		t.Fatal("direct Go release bypassed document byte budget")
	}
	manifest := IngressManifest{Version: Version, TopologySHA256: strings.Repeat("b", 64)}
	for i := 0; i < 3; i++ {
		binding := Binding{ID: fmt.Sprintf("zone%d", i), Host: fmt.Sprintf("zone%d.test", i)}
		zone := IngressZone{Binding: binding, ActiveRelease: "A0"}
		for j := 0; j < MaxReleasesPerZone; j++ {
			spec := testSpec(binding.ID, "", fmt.Sprintf("A%d", j))
			spec.Zone = binding
			spec.PublicOrigin = "https://" + binding.Host
			for k := 0; k < 8; k++ {
				spec.DocumentPaths = append(spec.DocumentPaths, "/"+strings.Repeat("x", 7998)+fmt.Sprint(k))
			}
			zone.Releases = append(zone.Releases, IngressRelease{Artifact: ReleaseManifest{Release: spec, BinarySHA256: strings.Repeat("c", 64)}, Origin: "http://backend.test"})
		}
		manifest.Zones = append(manifest.Zones, zone)
	}
	data, err := json.Marshal(manifest)
	if err != nil || len(data) <= MaxManifestBytes {
		t.Fatal("fixture did not exceed manifest budget", len(data), err)
	}
	if _, err := NormalizeIngressManifest(manifest); err == nil {
		t.Fatal("direct Go inventory bypassed encoded byte budget")
	}
	if _, err := ParseIngressManifest(data); err == nil {
		t.Fatal("JSON inventory bypassed byte budget")
	}
}
