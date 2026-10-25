//go:build !js

package offline

import (
	"bytes"
	"errors"
	"testing"
)

func TestNativeCodecRejectsZeroCatalog(t *testing.T) {
	for _, c := range []*Catalog{nil, new(Catalog)} {
		if data, err := MarshalCatalog(c); !errors.Is(err, ErrInvalid) || data != nil {
			t.Fatal("invalid catalog serialized", string(data), err)
		}
	}
}

func TestNativeCodecRoundTripsCanonicalCatalog(t *testing.T) {
	c := mustCatalog(t, sampleSpec(1000))
	data, err := MarshalCatalog(c)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseCatalog(data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Generation() != c.Generation() || parsed.Cost() != c.Cost() {
		t.Fatal("codec changed identity")
	}
	data[0] = '!'
	if parsed.Generation() != c.Generation() {
		t.Fatal("encoded buffer retained")
	}
}

func TestNativeCodecRejectsAmbiguousUnknownOrUnboundedJSON(t *testing.T) {
	data, err := MarshalCatalog(mustCatalog(t, sampleSpec(1000)))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"duplicate":        bytes.Replace(data, []byte(`"Version":1`), []byte(`"Version":1,"Version":1`), 1),
		"case-alias":       bytes.Replace(data, []byte(`"Version":1`), []byte(`"version":1`), 1),
		"nested-duplicate": bytes.Replace(data, []byte(`"ID":"public-guide"`), []byte(`"ID":"public-guide","ID":"public-guide"`), 1),
		"unknown":          bytes.Replace(data, []byte(`"Version":1`), []byte(`"Unknown":true,"Version":1`), 1),
		"null":             bytes.Replace(data, []byte(`"Semantics":"saved-anonymous-public"`), []byte(`"Semantics":null`), 1),
		"trailing":         append(append([]byte(nil), data...), []byte(` {}`)...),
		"oversized":        bytes.Repeat([]byte(" "), MaxCatalogBytes+1),
		"invalid-utf8":     append(append([]byte(nil), data...), 0xff),
		"deep":             []byte(`{"Config":{"Pages":[[[[[[[[[{}]]]]]]]]]}}`),
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			if c, err := ParseCatalog(bad); err == nil || c != nil {
				t.Fatal("invalid JSON accepted")
			}
		})
	}
}
