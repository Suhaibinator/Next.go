package navigation

import (
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

func TestStaticShellDocumentPathFiniteFinalDecodedSuffixRule(t *testing.T) {
	for _, suffix := range []string{".json", ".wasm", ".css", ".js", ".mjs", ".cjs", ".map"} {
		for _, spelling := range []string{suffix, strings.ToUpper(suffix), "%2E" + strings.ToUpper(suffix[1:]), "." + "%" + strings.ToUpper(stringHex(suffix[1])) + suffix[2:]} {
			for _, trailing := range []string{"", "/"} {
				pathname := "/workspace/report" + spelling + trailing
				if StaticShellDocumentPath(pathname) {
					t.Fatalf("resource spelling admitted: %q", pathname)
				}
			}
		}
	}
	for _, pathname := range []string{
		"/", "/workspace", "/workspace/", "/workspace/notes.v2", "/workspace/notes.v2/",
		"/workspace/notes.json/details", "/workspace/notes.JSON/details/", "/workspace/notes%2Ejson/details",
		"/workspace/report.jsonx", "/workspace/report.json.backup", "/workspace/reportwasm", "/workspace/report.",
		"/workspace/report%252EJSON", "/workspace/report%252ejson/", "/workspace/report%25%32%45json",
		"/workspace/%E6%9D%B1%E4%BA%AC", "/workspace/file.txt", "/workspace/file.svg", "/workspace/file.pdf",
		"/other/notes.v2", "/outside-scope/entry", "/_nextgo/entry", "/api/entry",
	} {
		if !StaticShellDocumentPath(pathname) {
			t.Fatalf("eligible path refused: %q", pathname)
		}
	}
	// Eligibility is independent of ownership. Resource-looking final segments
	// are refused even outside a declaration; other prefixes need their own
	// host/mount/manifest policy checks.
	for _, pathname := range []string{"/outside/report.json", "/_nextgo/runtime.wasm", "/api/result.Js"} {
		if StaticShellDocumentPath(pathname) {
			t.Fatal("suffix rule borrowed declaration scope", pathname)
		}
	}
}

func stringHex(value byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[value>>4], digits[value&15]})
}

func TestStaticShellDocumentPathRejectsMalformedPaths(t *testing.T) {
	for _, pathname := range []string{
		"", "workspace/report", "//workspace/report", "/workspace//report", "/workspace/report//", "/workspace//",
		"/workspace/.", "/workspace/..", "/workspace/%2e", "/workspace/%2e%2e", "/workspace/%2f", "/workspace/%5c",
		"/workspace/%00", "/workspace/%7f", "/workspace/%ff", "/workspace/%", "/workspace/\\report", "/workspace/\nreport", "/workspace/\rreport",
		"/workspace/report?x=1", "/workspace/report#part", "/workspace/report.json?x=1", "/workspace/report%2EJSON#part",
		"https://example.test/workspace/report", "/workspace/" + string([]byte{0xff}), "/" + strings.Repeat("x", 8192),
	} {
		if StaticShellDocumentPath(pathname) {
			t.Fatalf("malformed path admitted: %q", pathname)
		}
	}
}

func TestStaticShellDeclarationRejectsResourceZeroTailOnly(t *testing.T) {
	for _, suffix := range []string{".json", ".wasm", ".css", ".js", ".mjs", ".cjs", ".map"} {
		for _, spelling := range []string{suffix, strings.ToUpper(suffix)} {
			if _, err := ValidateStaticShellDeclaration("/workspace"+spelling+"/*rest?", "workspace"); err == nil {
				t.Fatal("resource-looking zero-tail seed admitted", spelling)
			}
		}
	}
	for _, pattern := range []string{"/workspace.v2/*rest?", "/workspace.json/details/*rest?", "/workspace%2EJSON/*rest?"} {
		if _, err := ValidateStaticShellDeclaration(pattern, "workspace"); err != nil {
			t.Fatal("eligible seed refused", pattern, err)
		}
	}
}

func TestStaticShellMatchTargetChecksResourceRuleBeforeSelection(t *testing.T) {
	deployment := paths.Config{BasePath: "/docs", TrailingSlash: true}
	d, _ := staticShellFixture(t, deployment)
	manifest := Manifest{Routes: []Route{{Pattern: d.Pattern}}}
	for _, pathname := range []string{"/docs/workspace/report.json", "/docs/workspace/report%2EJSON", "/docs/workspace/report.json/", "/docs/workspace/report.WASM", "/docs/workspace/report.cJs"} {
		if _, ok := d.MatchTarget(pathname, deployment, manifest); ok {
			t.Fatal("resource suffix selected shell", pathname)
		}
		// Shared parameter consumers deliberately remain suffix-agnostic.
		if _, ok := MatchRouteParams(d.Pattern, deployment.BasePath, pathname); !ok {
			t.Fatal("resource rule leaked into general params", pathname)
		}
	}
	for _, pathname := range []string{"/docs/workspace/notes.v2", "/docs/workspace/notes.json/details", "/docs/workspace/report%252EJSON"} {
		if _, ok := d.MatchTarget(pathname, deployment, manifest); !ok {
			t.Fatal("eligible virtual document refused", pathname)
		}
	}
}
