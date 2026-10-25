package navigation

import (
	"regexp"
	"strings"
	"testing"
)

func TestStreamIDPreservesLegacyByteGrammarAnd128ByteLimit(t *testing.T) {
	legacy := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,127}$`)
	values := []string{"", "a", "a:b", "a\n", "K", "aİ", string([]byte{'a', 0xff})}
	for _, n := range []int{127, 128, 129, 4096} {
		values = append(values, strings.Repeat("a", n))
	}
	for c := 0; c < 256; c++ {
		values = append(values, string([]byte{byte(c)}), "a"+string([]byte{byte(c)}), "Z"+strings.Repeat("a", 126)+string([]byte{byte(c)}))
	}
	for _, value := range values {
		if got, want := ValidStreamID(value), legacy.MatchString(value); got != want {
			t.Errorf("ValidStreamID(%q)=%v want%v", value, got, want)
		}
	}
}
