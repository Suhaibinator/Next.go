package client

import (
	"fmt"
	"strings"
	"testing"
)

func retainedFixtureInfo(index int) retainedInfo {
	return retainedInfo{ID: fmt.Sprintf("%032x", index), URL: fmt.Sprintf("https://site.test/%d", index), Partition: "public-demo", Fingerprint: strings.Repeat("a", 64), Nodes: 1000, Islands: 1, Bytes: 1024}
}

func TestRetentionAdmissionEvictsOldestHiddenViewWithoutMutatingOnFailure(t *testing.T) {
	var r retainedIndex
	for i := 1; i <= 3; i++ {
		entry := retainedFixtureInfo(i)
		if _, err := r.plan(entry); err != nil {
			t.Fatal(err)
		}
		r.put(entry)
	}
	old := retainedFixtureInfo(1)
	r.bind(strings.Repeat("f", 32), old.ID)
	latest := retainedFixtureInfo(4)
	evicted, err := r.plan(latest)
	if err != nil || len(evicted) != 1 || evicted[0] != old.ID || len(r.entries) != 3 || r.active != retainedFixtureInfo(3).ID {
		t.Fatal(evicted, err, r.active)
	}
	r.remove(evicted[0])
	r.put(latest)
	if len(r.entries) != 3 || len(r.aliases) != 0 {
		t.Fatal("evicted view retained its history authority")
	}
	invalid := retainedFixtureInfo(5)
	invalid.Nodes = retainedViewNodes + 1
	if _, err := r.plan(invalid); err == nil || len(r.entries) != 3 || r.active != latest.ID {
		t.Fatal("oversized candidate changed displayed ownership")
	}
}

func TestRetentionNodeAndMetadataBudgetsProtectTheActiveView(t *testing.T) {
	var r retainedIndex
	active := retainedFixtureInfo(1)
	active.Nodes = 4000
	active.Bytes = retainedMetadataBytes
	r.put(active)
	next := retainedFixtureInfo(2)
	if _, err := r.plan(next); err == nil || len(r.entries) != 1 || r.active != active.ID {
		t.Fatal("active view evicted speculatively")
	}
	active.Bytes = 1024
	r.put(active)
	next.Nodes = 4000
	if evictions, err := r.plan(next); err != nil || len(evictions) != 0 {
		t.Fatal(evictions, err)
	}
	r.put(next)
	third := retainedFixtureInfo(3)
	if evictions, err := r.plan(third); err != nil || len(evictions) != 1 || evictions[0] != active.ID {
		t.Fatal(evictions, err)
	}
}

func TestRetentionHistoryRequiresMatchingFreshAuthorityAndHasBoundedAliases(t *testing.T) {
	var r retainedIndex
	entry := retainedFixtureInfo(1)
	r.put(entry)
	key := strings.Repeat("b", 32)
	r.bind(key, entry.ID)
	if id, ok := r.lookup(key, entry.URL, entry.Partition, entry.Fingerprint); !ok || id != entry.ID {
		t.Fatal("valid history ownership lost")
	}
	for _, tc := range [][3]string{{entry.URL + "?new", entry.Partition, entry.Fingerprint}, {entry.URL, "other-user", entry.Fingerprint}, {entry.URL, entry.Partition, strings.Repeat("c", 64)}} {
		if _, ok := r.lookup(key, tc[0], tc[1], tc[2]); ok {
			t.Fatal("stale URL/privacy/output reused")
		}
	}
	for i := 1; i <= 200; i++ {
		r.bind(fmt.Sprintf("%032x", i), entry.ID)
	}
	if len(r.aliases) != retainedHistoryLimit || len(r.aliasOrder) != retainedHistoryLimit {
		t.Fatal("unbounded history metadata")
	}
	r.remove(entry.ID)
	if len(r.aliases) != 0 || len(r.aliasOrder) != 0 || len(r.entries) != 0 {
		t.Fatal("eviction kept retained ownership")
	}
}
