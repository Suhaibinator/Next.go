package scripts

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestDataAttributesSnapshotCanonicalIdentityAndWireRoundTrip(t *testing.T) {
	input := map[string]string{"data-domain": "example.test", "data-label": `Go "owned" <chart> & café`}
	data, err := NewDataAttributes(input)
	if err != nil {
		t.Fatal(err)
	}
	input["data-domain"] = "changed.test"
	values, err := data.Values()
	if err != nil || values["data-domain"] != "example.test" {
		t.Fatal("input mutation changed immutable attributes", err)
	}
	values["data-domain"] = "also-changed.test"
	s := Script{ID: "vendor", Src: "/vendor.js", Data: data}
	checked, err := s.Check()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(checked)
	decoded, parseErr := Parse(string(raw))
	if err != nil || parseErr != nil || decoded != checked {
		t.Fatal("comparable descriptor/wire roundtrip", err, parseErr)
	}
	alternate := checked
	alternate.Data = DataAttributes(`{"data-label":"Go \"owned\" <chart> & café","data-domain":"example.test"}`)
	alternate, err = alternate.Check()
	if err != nil || alternate.Data != data || alternate.Key() != checked.Key() {
		t.Fatal("attribute order changed execution identity", err)
	}
	changed, _ := NewDataAttributes(map[string]string{"data-domain": "different.test"})
	alternate.Data = changed
	if alternate.Key() == checked.Key() {
		t.Fatal("different vendor settings shared identity")
	}
	bare, err := json.Marshal(Script{ID: "bare", Src: "/bare.js"})
	if err != nil || strings.Contains(string(bare), `"data"`) {
		t.Fatal("empty data changed existing descriptor wire", string(bare), err)
	}
}

func TestDataAttributesRejectReservedExecutableMalformedAndUnboundedInput(t *testing.T) {
	for _, name := range []string{"onclick", "src", "nonce", "integrity", "data-", "data--name", "data-UPPER", "data-nextgo", "data-nextgo-script", "data-a\"b", "data-ä", "data-" + strings.Repeat("a", 60)} {
		if _, err := NewDataAttributes(map[string]string{name: "value"}); err == nil {
			t.Fatal("accepted invalid attribute", name)
		}
	}
	for _, value := range []string{strings.Repeat("x", 1025), "invalid\xff", "line\nbreak", "null\x00byte", "tab\tvalue", "delete\x7f"} {
		if _, err := NewDataAttributes(map[string]string{"data-value": value}); err == nil {
			t.Fatal("accepted invalid attribute value")
		}
	}
	values := map[string]string{}
	for i := 0; i < 17; i++ {
		values[fmt.Sprintf("data-%d", i)] = ""
	}
	if _, err := NewDataAttributes(values); err == nil {
		t.Fatal("accepted more than 16 attributes")
	}
	if _, err := NewDataAttributes(map[string]string{"data-escaped": strings.Repeat("<", 1024)}); err == nil {
		t.Fatal("ignored encoded byte bound")
	}
	for _, raw := range []string{`null`, `[]`, `{"onclick":"bad"}`, `{"data-a":1}`, `{"data-a":null}`, `{"data-a":"x","data-a":"y"}`, `{"data-nextgo-script":"fake"}`} {
		if _, err := DataAttributes(raw).Check(); err == nil {
			t.Fatal("accepted invalid encoded data", raw)
		}
		if _, err := Parse(`{"id":"v","src":"/v.js","data":` + raw + `}`); err == nil {
			t.Fatal("wire accepted invalid data", raw)
		}
	}
	oversized := Script{ID: "v", Src: "/" + strings.Repeat("&", 2000)}
	if _, err := oversized.Check(); err == nil {
		t.Fatal("escaped descriptor exceeded wire bound")
	}
}
