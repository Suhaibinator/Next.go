package navigation

import (
	"encoding/json"
	"html"
	"strconv"
	"strings"
	"testing"
)

func mergeFixture(t *testing.T) (*MergeDescriptor, string) {
	t.Helper()
	props := `{"price":"<€&\\\"","query":"?q=a&q="}`
	fallback := `<p>Useful &lt;fresh&gt; €</p>`
	prefix := `<div data-nextgo-client="1" data-nextgo-island="panel" ` + MergeNodeAttribute + `="1" data-nextgo-props="` + html.EscapeString(props) + `">`
	root := prefix + fallback + `</div>`
	body := "outside €" + root + "tail"
	start := len("outside €")
	d := &MergeDescriptor{Version: 1, Partition: "public", BodySHA256: MergeDigest(body), Owners: []MergeOwner{{Kind: "page", ID: MergeIdentity("test", "/shop/:id", "1")}}, Islands: []MergeIsland{{Node: 1, Owner: 1, Key: "editor", Component: "panel", Schema: "v1", Start: start, BodyStart: start + len(prefix), BodyEnd: start + len(prefix) + len(fallback), End: start + len(root), PropsSHA256: MergeDigest(props), FallbackSHA256: MergeDigest(fallback)}}}
	if d.Validate(body) != nil {
		t.Fatal("invalid fixture")
	}
	return d, body
}
func TestMergeRawRangesEscapeAndIndependentIdentity(t *testing.T) {
	d, body := mergeFixture(t)
	encoded, _ := json.Marshal(d)
	parsed, err := ParseMerge(string(encoded), body)
	if err != nil || parsed == nil {
		t.Fatal(err)
	}
	before, _ := d.Address(d.Islands[0])
	changed := *d
	changed.Partition = "other"
	changed.BodySHA256 = MergeDigest("other")
	changed.Islands = append([]MergeIsland{}, d.Islands...)
	changed.Islands[0].PropsSHA256 = MergeDigest("other")
	after, _ := changed.Address(changed.Islands[0])
	if before != after {
		t.Fatal("render/privacy data entered structural address")
	}
	changed.Islands[0].Schema = "v2"
	after, _ = changed.Address(changed.Islands[0])
	if before == after {
		t.Fatal("schema did not reset address")
	}
	if parsed.Validate(strings.Replace(body, "&lt;", "&#60;", 1)) == nil {
		t.Fatal("accepted normalized rather than exact bytes")
	}
}
func TestMergeDescriptorRejectsMalformedWireAndSlices(t *testing.T) {
	d, body := mergeFixture(t)
	encoded, _ := json.Marshal(d)
	valid := string(encoded)
	for _, raw := range []string{"null", "[]", "{}", valid + " {}", strings.Replace(valid, `"version":1`, `"Version":1`, 1), strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1), strings.Replace(valid, `"version":1`, `"version":1.0`, 1), strings.Replace(valid, `"parent":0`, `"parent":null`, 1), strings.Replace(valid, `"parent":0`, `"parent":1`, 1), strings.Replace(valid, `"owner":1`, `"owner":null`, 1), strings.Replace(valid, `"schema":"v1"`, `"schema":null`, 1), strings.Replace(valid, `"owners":[`, `"unknown":1,"owners":[`, 1), strings.Repeat(" ", MaxMergeDescriptor+1)} {
		if _, err := ParseMergeDescriptor(raw); err == nil {
			t.Fatalf("accepted %.120q", raw)
		}
	}
	for _, mutate := range []func(*MergeDescriptor){func(v *MergeDescriptor) { v.Islands[0].BodyStart++ }, func(v *MergeDescriptor) { v.Islands[0].BodyEnd-- }, func(v *MergeDescriptor) { v.Islands[0].End = len(body) + 1 }, func(v *MergeDescriptor) { v.Islands[0].Node = 0 }, func(v *MergeDescriptor) { v.Islands[0].Owner = 2 }, func(v *MergeDescriptor) { v.Islands[0].Schema = "bad schema" }, func(v *MergeDescriptor) { v.Islands = append(v.Islands, v.Islands[0]) }, func(v *MergeDescriptor) { v.Owners[0].Kind = "keyed" }} {
		var value MergeDescriptor
		_ = json.Unmarshal(encoded, &value)
		mutate(&value)
		if value.Validate(body) == nil {
			t.Fatal("accepted corrupt ownership/slice")
		}
	}
	for _, replacement := range []string{strings.Replace(body, "Useful", "Staler", 1), strings.Replace(body, `data-nextgo-client="1"`, `data-nextgo-client="2"`, 1), strings.Replace(body, "€", string([]byte{255}), 1)} {
		var value MergeDescriptor
		_ = json.Unmarshal(encoded, &value)
		value.BodySHA256 = MergeDigest(replacement)
		if value.Validate(replacement) == nil {
			t.Fatal("body hash alone authorized malformed inner data/root")
		}
	}
	if value, err := ParseMerge("", body); err != nil || value != nil {
		t.Fatal(value, err)
	}
}
func TestMergeDeclaredParentKeysAndFraming(t *testing.T) {
	d, _ := mergeFixture(t)
	page := d.Owners[0]
	d.Owners = append(d.Owners, MergeOwner{Parent: 1, Kind: "keyed", ID: "a", Tag: "article"}, MergeOwner{Parent: 1, Kind: "keyed", ID: "b", Tag: "article"})
	a := d.Islands[0]
	a.Owner = 2
	b := a
	b.Owner = 3
	one, _ := d.Address(a)
	two, _ := d.Address(b)
	if one == two {
		t.Fatal("global key collision")
	}
	d.Owners[0] = page
	if MergeIdentity("d", "ab", "c") == MergeIdentity("d", "a", "bc") {
		t.Fatal("unframed tuple collision")
	}
	for depth := 0; depth < MaxMergeDepth; depth++ {
		d.Owners = append(d.Owners, MergeOwner{Parent: len(d.Owners), Kind: "keyed", ID: strconv.Itoa(depth), Tag: "div"})
	}
	a.Owner = len(d.Owners)
	if _, err := d.Address(a); err == nil {
		t.Fatal("unbounded ancestry")
	}
}
