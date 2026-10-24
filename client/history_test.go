package client

import (
	"fmt"
	"strings"
	"testing"
)

func TestHistoryRegistryBoundsAndExactEntryOwnership(t *testing.T) {
	var registry historyRegistry
	for i := 0; i < historyRecordLimit+5; i++ {
		registry.put(fmt.Sprint(i), historyRecord{URL: fmt.Sprint("https://site.test/", i), Previous: "https://site.test/gallery"})
	}
	if len(registry.records) != historyRecordLimit || len(registry.order) != historyRecordLimit {
		t.Fatal(len(registry.records), len(registry.order))
	}
	if _, ok := registry.get("0", "https://site.test/0"); ok {
		t.Fatal("evicted entry remained owned")
	}
	key := fmt.Sprint(historyRecordLimit + 4)
	url := fmt.Sprint("https://site.test/", historyRecordLimit+4)
	if _, ok := registry.get(key, url); !ok {
		t.Fatal("new entry was lost")
	}
	if record, ok := registry.get(key, "https://site.test/other"); ok || record.Previous != "" {
		t.Fatal("key for a different URL supplied history", record, ok)
	}
	if registry.put("oversized", historyRecord{URL: strings.Repeat("x", historyURLLimit+1)}) {
		t.Fatal("oversized URL entered history registry")
	}
	registry.put(key, historyRecord{URL: url, Previous: "https://site.test/new"})
	if len(registry.order) != historyRecordLimit {
		t.Fatal("updates append duplicate ownership")
	}
}

func TestReturnTargetMatchesPreservesExistingFragmentsButBindsQueryAndOrigin(t *testing.T) {
	for _, tc := range []struct {
		previous, target string
		want             bool
	}{
		{"https://site.test/gallery?q=x#counter", "https://site.test/gallery?q=x", true},
		{"https://site.test/gallery#one", "https://site.test/gallery#two", false},
		{"https://site.test/gallery?q=one", "https://site.test/gallery?q=two", false},
		{"https://other.test/gallery", "https://site.test/gallery", false},
		{"/gallery", "https://site.test/gallery", false},
	} {
		if got := returnTargetMatches(tc.previous, tc.target); got != tc.want {
			t.Error(tc, got)
		}
	}
}
