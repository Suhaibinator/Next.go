package navigation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

func TestRetentionBindsExactServerMarkupAndPartition(t *testing.T) {
	html := `<p data-label="a&amp;b">A &lt; B</p>`
	value, err := NewRetention("public-demo-v1", html)
	if err != nil || value == nil || value.ValidateDescriptor() != nil || value.Validate(html) != nil {
		t.Fatal(value, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRetention(string(encoded), html)
	if err != nil || parsed == nil || *parsed != *value {
		t.Fatal("round trip lost identity", parsed, err)
	}
	if _, err := ParseRetention(string(encoded), strings.ReplaceAll(html, "&amp;", "&#38;")); err == nil {
		t.Fatal("normalized markup replaced the exact server bytes")
	}
	other, err := NewRetention("public-demo-v2", html)
	if err != nil || other.Fingerprint != value.Fingerprint || other.Partition == value.Partition {
		t.Fatal("partition and body identity were conflated", other, err)
	}
	var absent *Retention
	if absent.ValidateDescriptor() == nil || absent.Validate(html) == nil {
		t.Fatal("nil metadata was accepted as an explicit descriptor")
	}
}

func TestRetentionDescriptorIsClosedAndBounded(t *testing.T) {
	value, _ := NewRetention("demo", "body")
	encoded, _ := json.Marshal(value)
	valid := string(encoded)
	for _, raw := range []string{
		"null", "[]", "{}", valid + " {}", valid[:len(valid)-1] + `,"extra":true}`,
		strings.Replace(valid, `"version":1`, `"Version":1`, 1),
		strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(valid, `"version":1`, `"version":1.0`, 1),
		strings.Replace(valid, `"version":1`, `"version":2`, 1),
		strings.Replace(valid, `"partition":"demo"`, `"partition":null`, 1),
		strings.Replace(valid, `"fingerprint":"`, `"fingerprint":"G`, 1),
		strings.Replace(valid, `"partition":"demo"`, "\"partition\":\"\xff\"", 1),
		strings.Repeat(" ", MaxRetentionDescriptor+1),
	} {
		if _, err := ParseRetentionDescriptor(raw); err == nil {
			t.Fatalf("accepted malformed explicit descriptor: %.120q", raw)
		}
	}
	if value, err := ParseRetention("", "body"); err != nil || value != nil {
		t.Fatal("absent metadata stopped ordinary rendering", value, err)
	}
	for _, partition := range []string{"", "with space", "line\n", "\x7f", "é", strings.Repeat("a", MaxRetentionPartition+1)} {
		if _, err := NewRetention(partition, "body"); err == nil {
			t.Fatal("accepted invalid partition")
		}
	}
	if value, err := NewRetention(strings.Repeat("<", MaxRetentionPartition), strings.Repeat("a", MaxRetentionHTML)); err != nil || value == nil || value.ValidateDescriptor() != nil {
		t.Fatal("valid upper bounds were rejected", err)
	}
	if value, err := NewRetention("demo", strings.Repeat("a", MaxRetentionHTML+1)); err != nil || value != nil {
		t.Fatal("oversized valid page lost the cold-render fallback", value, err)
	}
}

func TestRetentionOptInContributesToConfigurationIdentityWithoutDefaultBytes(t *testing.T) {
	plain := Manifest{Routes: []Route{{Pattern: "/a"}}}
	encoded, _ := json.Marshal(plain)
	if strings.Contains(string(encoded), "retainRoutes") {
		t.Fatal("default manifest acquired an extra field")
	}
	retained := plain
	retained.RetainRoutes = true
	if ConfigurationID("build", paths.Config{}, plain, false, false) == ConfigurationID("build", paths.Config{}, retained, false, false) {
		t.Fatal("retention policy did not bind the runtime configuration")
	}
}
