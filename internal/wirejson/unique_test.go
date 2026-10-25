package wirejson

import (
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/wirejson/scan"
)

func TestUniqueForwardsLeaf(t *testing.T) {
	for _, data := range [][]byte{
		[]byte(`null`),
		[]byte(` {"count":123456789012345678901234567890e999999} `),
		[]byte("\"\xff\""),
		[]byte(`"\ud800"`),
		[]byte(strings.Repeat("[", 64) + "null" + strings.Repeat("]", 64)),
	} {
		if err := Unique(data); err != nil {
			t.Fatalf("forwarded valid value %q: %v", data, err)
		}
	}
	for _, test := range []struct {
		name string
		data []byte
		want string
	}{
		{"syntax", []byte(`{"missing":`), "invalid JSON"},
		{"trailing", []byte(`null true`), "invalid JSON"},
		{"escaped duplicate", []byte(`{"key":1,"\u006bey":2}`), "duplicate or invalid object key"},
		{"replacement duplicate", []byte("{\"\xff\":1,\"\\ufffd\":2}"), "duplicate or invalid object key"},
		{"depth", []byte(strings.Repeat("[", 65) + "null" + strings.Repeat("]", 65)), "JSON nesting exceeds 64"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, leaf := Unique(test.data), scan.Unique(test.data)
			if got == nil || got != leaf || got.Error() != test.want {
				t.Fatalf("Unique(%q) = %v; leaf = %v, want unchanged %q", test.data, got, leaf, test.want)
			}
		})
	}
}

// Strict remains the standard decoder API. Extracting Unique must not make
// Strict itself reject duplicate keys or null, or weaken its shape/EOF checks.
func TestStrictCompatibilityAfterLeafExtraction(t *testing.T) {
	for _, test := range []struct {
		data string
		want int
		fail bool
	}{
		{`{"value":1}`, 1, false},
		{`{"value":1,"value":2}`, 2, false},
		{`null`, 0, false},
		{`{"value":1,"unknown":2}`, 0, true},
		{`{"value":1} null`, 0, true},
	} {
		t.Run(test.data, func(t *testing.T) {
			var target struct {
				Value int `json:"value"`
			}
			err := Strict([]byte(test.data), &target)
			if (err != nil) != test.fail || !test.fail && target.Value != test.want {
				t.Fatalf("Strict(%q) = value %d, error %v; want value %d, failure %v", test.data, target.Value, err, test.want, test.fail)
			}
		})
	}
}
