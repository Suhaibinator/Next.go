package offline

import (
	"errors"
	"sync"
	"testing"
)

func stored(c *Catalog) *Stored { return &Stored{Catalog: c, Inventory: observations(c)} }

func completeStage(t *testing.T, l *Ledger, token Reservation, c *Catalog) {
	t.Helper()
	for _, item := range observations(c) {
		if err := l.Observe(token, item); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Complete(token); err != nil {
		t.Fatal(err)
	}
}

func TestReservationsChargeActivePreviousAndStaging(t *testing.T) {
	a, b, c := mustCatalog(t, sampleSpec(1000)), mustCatalog(t, sampleSpec(2000)), mustCatalog(t, sampleSpec(3000))
	l, err := Restore(a.Policy(), stored(b), stored(a))
	if err != nil {
		t.Fatal(err)
	}
	token, err := l.Reserve(c)
	if err != nil {
		t.Fatal(err)
	}
	s := l.Snapshot()
	if s.Reserved.Bytes != a.Cost().Bytes+b.Cost().Bytes+c.Cost().Bytes || s.Reserved.Entries != a.Cost().Entries+b.Cost().Entries+c.Cost().Entries {
		t.Fatal("stage or previous uncharged", s)
	}
	if err := l.Complete(token); !errors.Is(err, ErrIncomplete) {
		t.Fatal("marker bypassed inventory", err)
	}
	if err := l.Activate(token, true); !errors.Is(err, ErrState) {
		t.Fatal("incomplete stage activated", err)
	}
	completeStage(t, l, token, c)
	if err := l.Activate(token, false); !errors.Is(err, ErrState) {
		t.Fatal("live-client activation allowed", err)
	}
	if err := l.Activate(token, true); !errors.Is(err, ErrState) {
		t.Fatal("previous removed implicitly", err)
	}
	if l.Snapshot().Active != b.Generation() {
		t.Fatal("last good changed on refusal")
	}
	if err := l.ConfirmPreviousRemoved("wrong"); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	if err := l.ConfirmPreviousRemoved(a.Generation()); err != nil {
		t.Fatal(err)
	}
	if err := l.Activate(token, true); err != nil {
		t.Fatal(err)
	}
	s = l.Snapshot()
	if s.Active != c.Generation() || s.Previous != b.Generation() || s.Staging != "" || s.Reserved.Bytes != b.Cost().Bytes+c.Cost().Bytes {
		t.Fatal(s)
	}
	if err := l.ConfirmStagingRemoved(token); !errors.Is(err, ErrState) {
		t.Fatal("stale reservation reused")
	}
}

func TestQuotaRefusalPreservesLastGoodAndNoImplicitEviction(t *testing.T) {
	makeBounded := func(saved int64) *Catalog {
		s := sampleSpec(saved)
		s.Config.Budget = Budget{TotalBytes: 20 << 20, GenerationBytes: 12 << 20, AssetBytes: 12 << 20}
		return mustCatalog(t, s)
	}
	a, b, c := makeBounded(1000), makeBounded(2000), makeBounded(3000)
	l, err := Restore(a.Policy(), stored(b), stored(a))
	if err != nil {
		t.Fatal(err)
	}
	before := l.Snapshot()
	if _, err := l.Reserve(c); !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
	if after := l.Snapshot(); after != before {
		t.Fatal("quota refusal changed last good", before, after)
	}
	// Exact per-generation payload reuse still consumes physical cache storage.
	if before.Reserved.Bytes != 2*a.Cost().Bytes {
		t.Fatal("cross-generation dedup assumed")
	}
}

func TestInterruptedStageRemainsChargedUntilPhysicalRemovalConfirmation(t *testing.T) {
	a, b, c := mustCatalog(t, sampleSpec(1000)), mustCatalog(t, sampleSpec(2000)), mustCatalog(t, sampleSpec(3000))
	items := observations(b)
	l, err := Restore(a.Policy(), stored(a), nil, &Stored{Catalog: b, Inventory: items[:2]})
	if err != nil {
		t.Fatal(err)
	}
	token, ok := l.Pending()
	if !ok {
		t.Fatal("interrupted stage forgotten")
	}
	if l.Snapshot().Reserved.Bytes != a.Cost().Bytes+b.Cost().Bytes {
		t.Fatal("interrupted stage uncharged")
	}
	if _, err := l.Reserve(c); !errors.Is(err, ErrState) {
		t.Fatal("second stage admitted")
	}
	if err := l.Complete(token); !errors.Is(err, ErrIncomplete) {
		t.Fatal("partial restore treated marker as completion")
	}
	if err := l.Observe(token, items[0]); !errors.Is(err, ErrIncomplete) {
		t.Fatal("duplicate integrity evidence accepted")
	}
	for _, item := range items[2:] {
		if err := l.Observe(token, item); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Complete(token); err != nil {
		t.Fatal(err)
	}
	if err := l.ConfirmStagingRemoved(token); err != nil {
		t.Fatal(err)
	}
	if l.Snapshot().Active != a.Generation() || l.Snapshot().Reserved.Bytes != a.Cost().Bytes {
		t.Fatal("cleanup disturbed active")
	}
	if _, err := l.Reserve(c); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreRequiresCompleteCurrentAndPreviousInventories(t *testing.T) {
	a, b := mustCatalog(t, sampleSpec(1000)), mustCatalog(t, sampleSpec(2000))
	bad := stored(a)
	bad.Inventory = bad.Inventory[:len(bad.Inventory)-1]
	for name, pair := range map[string][2]*Stored{"active": {bad, nil}, "previous": {stored(b), bad}, "previous-without-active": {nil, stored(a)}} {
		t.Run(name, func(t *testing.T) {
			if l, err := Restore(a.Policy(), pair[0], pair[1]); err == nil || l != nil {
				t.Fatal("invalid restoration admitted")
			}
		})
	}
	foreign := sampleSpec(3000)
	foreign.Config.ID = "different-owner"
	if _, err := Restore(a.Policy(), stored(a), stored(mustCatalog(t, foreign))); !errors.Is(err, ErrInvalid) {
		t.Fatal("foreign owner restored")
	}
}

func TestReservationHandlesCannotCrossLedgersAndOnlyOneStageWins(t *testing.T) {
	a, b := mustCatalog(t, sampleSpec(1000)), mustCatalog(t, sampleSpec(2000))
	l1, _ := Restore(a.Policy(), stored(a), nil)
	l2, _ := Restore(a.Policy(), stored(a), nil)
	token, _ := l1.Reserve(b)
	_, _ = l2.Reserve(b)
	if err := l2.Observe(token, observations(b)[0]); !errors.Is(err, ErrState) {
		t.Fatal("cross-ledger handle accepted")
	}
	l, _ := Restore(a.Policy(), stored(a), nil)
	var wg sync.WaitGroup
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := l.Reserve(b); results <- err }()
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrState) {
			t.Fatal(err)
		}
	}
	if winners != 1 || l.Snapshot().Reserved.Bytes != a.Cost().Bytes+b.Cost().Bytes {
		t.Fatal("reservation race", winners, l.Snapshot())
	}
}
