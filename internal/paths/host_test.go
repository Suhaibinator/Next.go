//go:build !js

package paths

import "testing"

func TestCanonicalHostPreservesExactDomainAuthorityWithoutAcceptingRedirectInputs(t *testing.T) {
	for input, want := range map[string]string{"FR.Example.:8443": "fr.example:8443", "[2001:db8::1]:443": "[2001:db8::1]:443", "127.0.0.1:4124": "127.0.0.1:4124", "example.test.": "example.test"} {
		got, err := CanonicalHost(input)
		if err != nil || got != want {
			t.Fatal(input, got, want, err)
		}
	}
	for _, input := range []string{"", "https://example.test", "user@example.test", "example.test/path", "example.test?query", "example.test#fragment", "*.example.test", "example.test\r\nX:yes", "example.test:0", "example.test:65536", "[::1]:", ".example.test", "a..test", "-a.test", "éxample.test"} {
		if got, err := CanonicalHost(input); err == nil {
			t.Fatal("unsafe host accepted", input, got)
		}
	}
}
