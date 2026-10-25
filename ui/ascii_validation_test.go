package ui

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

func TestElementNameKeepsUnicodeTagNormalizationAndRawAttributeValidation(t *testing.T) {
	for _, tc := range []struct{ tag, want string }{{"K", "<k></k>"}, {"İ", "<i></i>"}, {"KUSTOM", "<kustom></kustom>"}, {"X:Custom_9-", "<x:custom_9-></x:custom_9->"}} {
		html, err := HTML(El(tc.tag, nil))
		if err != nil || html != tc.want {
			t.Fatal(tc, html, err)
		}
	}
	for _, tag := range []string{"SCRİPT", "STYLE", "1tag", "_tag", "a\n", string([]byte{'x', 0xff})} {
		if _, err := HTML(El(tag, nil)); err == nil {
			t.Fatal("unsupported tag accepted", tag)
		}
	}
	for _, key := range []string{"K", "İ", "OnClick", "STYLE", "http-equiv", "data\n", string([]byte{'x', 0xff})} {
		if _, err := HTML(El("div", Attrs{key: "value"})); err == nil {
			t.Fatal("unsupported/raw Unicode attribute accepted", key)
		}
	}
	value, err := HTML(El("div", Attrs{"Data-FOO:bar": "<&"}))
	if err != nil || value != `<div Data-FOO:bar="&lt;&amp;"></div>` {
		t.Fatal(value, err)
	}
	// HTML names were never capped at streaming IDs'128-byte limit.
	long := strings.Repeat("a", 4096)
	if _, err := HTML(El(long, Attrs{long: "v"})); err != nil {
		t.Fatal("new HTML name length limit", err)
	}
}

func TestElementAndAttributeGrammarMatchesLegacyForEveryByte(t *testing.T) {
	legacy := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9:_-]*$`)
	for c := 0; c < 256; c++ {
		raw := "x" + string([]byte{byte(c)}) + "Z"
		_, err := HTML(El(raw, nil))
		if got, want := err == nil, legacy.MatchString(strings.ToLower(raw)); got != want {
			t.Errorf("tag%q accepted%v want%v", raw, got, want)
		}
		_, err = HTML(El("div", Attrs{"data-" + raw: "value"}))
		if got, want := err == nil, legacy.MatchString("data-"+raw); got != want {
			t.Errorf("attribute%q accepted%v want%v", raw, got, want)
		}
	}
}

func TestDeferredAndRequestRegionIDPreserveLegacyGrammarAndBounds(t *testing.T) {
	legacy := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	values := []string{"", "a", "Z_9-", "0a", "_a", "a:b", "a\n", "K", "aİ", string([]byte{'a', 0xff}), strings.Repeat("a", 128), strings.Repeat("a", 129)}
	for c := 0; c < 256; c++ {
		values = append(values, string([]byte{byte(c)}), "a"+string([]byte{byte(c)}))
	}
	for _, id := range values {
		want := len(id) <= 128 && legacy.MatchString(id)
		if got := validRegionName(id); got != want {
			t.Errorf("region%q accepted%v want%v", id, got, want)
		}
		called := false
		_, err := HTML(Defer(id, Text("fallback"), func(context.Context) (Node, error) { called = true; return Text("loaded"), nil }))
		if (err == nil) != want || called != want {
			t.Errorf("deferred%q: error%v called%v want%v", id, err, called, want)
		}
	}
}
