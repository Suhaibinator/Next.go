package navigation

import (
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

func TestConfigurationIdentityCoversRuntimeOwnershipRules(t *testing.T) {
	manifest := Manifest{Routes: []Route{{Pattern: "/"}, {Pattern: "/download", Endpoint: true}}, Handlers: []string{"/robots.txt"}}
	original := ConfigurationID("build", paths.Config{}, manifest, false, false)
	if !ValidConfigurationID(original) || original != ConfigurationID("build", paths.Config{}, manifest, false, false) {
		t.Fatal("invalid or unstable identity", original)
	}
	variants := []string{
		ConfigurationID("build", paths.Config{}, manifest, false, true),
		ConfigurationID("other", paths.Config{}, manifest, false, false),
		ConfigurationID("build", paths.Config{BasePath: "/docs"}, manifest, false, false),
		ConfigurationID("build", paths.Config{AssetPrefix: "/cdn"}, manifest, false, false),
		ConfigurationID("build", paths.Config{AssetPrefix: "https://cdn.example.test"}, manifest, false, false),
		ConfigurationID("build", paths.Config{TrailingSlash: true}, manifest, false, false),
		ConfigurationID("build", paths.Config{}, manifest, true, false),
		ConfigurationID("build", paths.Config{}, Manifest{Routes: []Route{{Pattern: "/"}, {Pattern: "/download"}}, Handlers: manifest.Handlers}, false, false),
		ConfigurationID("build", paths.Config{}, Manifest{Routes: manifest.Routes, Handlers: []string{"/different"}}, false, false),
	}
	seen := map[string]bool{original: true}
	for _, variant := range variants {
		if seen[variant] || !ValidConfigurationID(variant) {
			t.Fatal("configuration change reused an identity", variant)
		}
		seen[variant] = true
	}
	for _, value := range []string{"", strings.Repeat("a", 63), strings.Repeat("g", 64), strings.ToUpper(original)} {
		if ValidConfigurationID(value) {
			t.Fatal("accepted malformed identity", value)
		}
	}
}
