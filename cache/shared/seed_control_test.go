package shared

import (
	"context"
	"testing"
)

func TestSeedControlHashesSeparateAllIdentityFields(t *testing.T) {
	ids := []ID{{"app", "schema", "key"}, {"app2", "schema", "key"}, {"app", "schema2", "key"}, {"app", "schema", "key2"}, {"ab", "c", "d"}, {"a", "bc", "d"}, {"a", "b", "cd"}}
	seen := map[[32]byte]bool{}
	for _, id := range ids {
		h := seedKeyHash(id)
		if seen[h] || h != seedKeyHash(id) {
			t.Fatal("unstable or ambiguous seed identity", id)
		}
		seen[h] = true
	}
}

func TestSeedControlCloneCopiesSeenMarkersOnlyWhenInstalling(t *testing.T) {
	ctx := context.Background()
	memory, err := NewMemoryWithOptions(MemoryOptions{Capacity: 8, MaxBytes: 1 << 20, MaxControlBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	_, session := seedAuditSession(t, memory)
	a := seedAuditValue("a", "a")
	if n, err := memory.InstallSeeds(ctx, "app", session.Token, []SeedValue{a}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	clone := cloneMemory(memory, memory.now())
	if !clone.seedSessions[session.Token].seenShared {
		t.Fatal("clone eagerly owns the once-per-key ledger")
	}
	b := seedAuditValue("b", "b")
	if n, err := clone.InstallSeeds(ctx, "app", session.Token, []SeedValue{b}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if len(memory.seedSessions[session.Token].seen) != 1 || len(clone.seedSessions[session.Token].seen) != 2 || clone.seedSessions[session.Token].seenShared {
		t.Fatal("candidate changed original ledger")
	}
	c := seedAuditValue("c", "c")
	if n, err := memory.InstallSeeds(ctx, "app", session.Token, []SeedValue{c}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, ok := clone.seedSessions[session.Token].seen[seedKeyHash(c.ID)]; ok {
		t.Fatal("original changed candidate ledger")
	}
	for _, m := range []*Memory{memory, clone} {
		u := m.Usage()
		if u.SeedSeenKeys != 2 || u.ControlBytes != seedSessionCost("app")+2*seedSeenCost {
			t.Fatal(u)
		}
	}
}
