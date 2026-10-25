package ascii_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/ascii"
)

var legacyHTML = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9:_-]*$`)
var legacyIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
var legacyHex = regexp.MustCompile(`^[a-f0-9]{64}$`)

func corpus() []string {
	values := []string{"", "a", "Z", "a0_Z-", "a:b", "0a", "_a", "-a", ":a", "a\n", "a\r", "a\x00", "a\x7f", "K", "İ", "ſ", "Ａ", "中", "e\u0301", "a\u200d", string([]byte{0xff}), string([]byte{'a', 0xc0, 0xaf})}
	for _, n := range []int{63, 64, 65, 127, 128, 129, 4096} {
		values = append(values, strings.Repeat("a", n))
	}
	for c := 0; c < 256; c++ {
		b := string([]byte{byte(c)})
		values = append(values, b, "a"+b, b+"Z", "A"+b+"Z", strings.Repeat(b, 64))
	}
	return values
}

func TestValidatorsDifferentialAllByteClassesAndBoundaries(t *testing.T) {
	for _, value := range corpus() {
		if got, want := ascii.HTMLName(value), legacyHTML.MatchString(value); got != want {
			t.Errorf("HTMLName(%q)=%v want%v", value, got, want)
		}
		if got, want := ascii.Identifier(value), legacyIdentifier.MatchString(value); got != want {
			t.Errorf("Identifier(%q)=%v want%v", value, got, want)
		}
		if got, want := ascii.LowerHex(value, 64), legacyHex.MatchString(value); got != want {
			t.Errorf("LowerHex(%q)=%v want%v", value, got, want)
		}
	}
	for _, length := range []int{0, 1, 2, 63, 64, 65} {
		reference := regexp.MustCompile(fmt.Sprintf(`^[a-f0-9]{%d}$`, length))
		for _, value := range []string{"", strings.Repeat("a", length), strings.Repeat("f", length) + "\n", strings.Repeat("F", length), "0"} {
			if got, want := ascii.LowerHex(value, length), reference.MatchString(value); got != want {
				t.Errorf("hex length%d %q=%v want%v", length, value, got, want)
			}
		}
	}
	if ascii.LowerHex("", -1) {
		t.Fatal("negative requested length accepted")
	}
}

func FuzzHTMLNameMatchesLegacy(f *testing.F) {
	for _, s := range corpus()[:30] {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if ascii.HTMLName(value) != legacyHTML.MatchString(value) {
			t.Fatalf("HTML grammar changed for%q", value)
		}
	})
}
func FuzzIdentifierMatchesLegacy(f *testing.F) {
	for _, s := range corpus()[:30] {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if ascii.Identifier(value) != legacyIdentifier.MatchString(value) {
			t.Fatalf("ID grammar changed for%q", value)
		}
	})
}
func FuzzLowerHexMatchesLegacy(f *testing.F) {
	for _, s := range corpus()[:30] {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if ascii.LowerHex(value, 64) != legacyHex.MatchString(value) {
			t.Fatalf("hex grammar changed for%q", value)
		}
	})
}
