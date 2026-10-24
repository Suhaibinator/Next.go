package shared

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

type controlAuditBackend interface {
	Backend
	SeedBackend
	Usage() Usage
}

func controlAuditBackends(t *testing.T, limit int64, run func(*testing.T, controlAuditBackend, func(time.Duration))) {
	t.Helper()
	for _, kind := range []string{"memory", "file"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Now().UTC()
			var b controlAuditBackend
			if kind == "memory" {
				m, err := NewMemoryWithOptions(MemoryOptions{Capacity: 1, MaxBytes: 1 << 20, MaxControlBytes: limit})
				if err != nil {
					t.Fatal(err)
				}
				m.now = func() time.Time { return now }
				b = m
			} else {
				f := openFileTest(t, fileAuditDir(t), FileOptions{Capacity: 1, MaxBytes: 1 << 20, MaxDiskBytes: 32768, MaxControlBytes: limit})
				f.now = func() time.Time { return now }
				f.memory.now = f.now
				b = f
			}
			run(t, b, func(d time.Duration) { now = now.Add(d) })
		})
	}
}

func controlAuditSession(t *testing.T, b SeedBackend, namespace string) SeedSession {
	t.Helper()
	ticket, err := b.ReserveSeeds(context.Background(), namespace)
	if err != nil {
		t.Fatal(err)
	}
	session, err := b.BeginSeeds(context.Background(), namespace, ticket.Token)
	if err != nil || session.Token == "" {
		t.Fatal(session, err)
	}
	return session
}

func controlAuditValue(namespace string, index int) SeedValue {
	return SeedValue{ID: ID{Namespace: namespace, Schema: "control-v1", Key: fmt.Sprintf("%04d-", index) + strings.Repeat("k", 4000)}, Policy: Policy{FreshFor: time.Hour, ExpireAfter: 2 * time.Hour, Tags: []string{"original-tag"}, Paths: []string{"/original"}}, Value: []byte("original")}
}

func controlAuditRead(t *testing.T, b Backend, id ID, policy Policy) View {
	t.Helper()
	v, err := b.Begin(context.Background(), id, policy, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestControlAuditRejectedBatchPreservesDataSeenAndEvictionOrder(t *testing.T) {
	controlAuditBackends(t, 1200, func(t *testing.T, b controlAuditBackend, _ func(time.Duration)) {
		ctx := context.Background()
		s := controlAuditSession(t, b, "app")
		kept := controlAuditValue("app", 0)
		if n, err := b.InstallSeeds(ctx, "app", s.Token, []SeedValue{kept}); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		before := b.Usage()
		batch := []SeedValue{controlAuditValue("app", 1), controlAuditValue("app", 2), controlAuditValue("app", 3), controlAuditValue("app", 4)}
		if n, err := b.InstallSeeds(ctx, "app", s.Token, batch); !errors.Is(err, ErrCapacity) || n != 0 {
			t.Fatal(n, err)
		}
		if after := b.Usage(); after != before {
			t.Fatal("rejected whole batch mutated accounting", before, after)
		}
		v := controlAuditRead(t, b, kept.ID, kept.Policy)
		if !v.Found || string(v.Value) != "original" {
			t.Fatal("control rejection evicted data", v)
		}
		if n, err := b.InstallSeeds(ctx, "app", s.Token, batch[:1]); err != nil || n != 1 {
			t.Fatal("rejected batch marked a key seen", n, err)
		}
		after := b.Usage()
		if n, err := b.InstallSeeds(ctx, "app", s.Token, batch[:1]); err != nil || n != 0 || b.Usage() != after {
			t.Fatal("retry consumed control capacity", n, err)
		}
		if n, err := b.InstallSeeds(ctx, "app", s.Token, []SeedValue{kept}); err != nil || n != 0 {
			t.Fatal("evicted generation was resurrected", n, err)
		}
		v = controlAuditRead(t, b, batch[0].ID, batch[0].Policy)
		if !v.Found {
			t.Fatal("denied reimport evicted current generation", v)
		}
	})
}

func TestControlAuditBeginCapacityRetainsPendingTicket(t *testing.T) {
	controlAuditBackends(t, 600, func(t *testing.T, b controlAuditBackend, _ func(time.Duration)) {
		ctx := context.Background()
		ticket, err := b.ReserveSeeds(ctx, "app")
		if err != nil {
			t.Fatal(err)
		}
		before := b.Usage()
		for i := 0; i < 3; i++ {
			if s, err := b.BeginSeeds(ctx, "app", ticket.Token); !errors.Is(err, ErrCapacity) || s.Token != "" {
				t.Fatal(s, err)
			}
			if after := b.Usage(); after != before || after.SeedTickets != 1 {
				t.Fatal("failed begin consumed ticket", before, after)
			}
		}
		// Data admission has an independent budget even when control cannot grow.
		value := controlAuditValue("app", 1)
		flight := acquire(t, b, value.ID, value.Policy)
		publish(t, b, value.ID, flight.Lease, []byte("ordinary runtime"))
		if view := controlAuditRead(t, b, value.ID, value.Policy); !view.Found || string(view.Value) != "ordinary runtime" {
			t.Fatal(view)
		}
		if after := b.Usage(); after.ControlBytes != before.ControlBytes || after.DataBytes == 0 || after.DataBytes > after.DataLimit {
			t.Fatal(after)
		}
	})
}

func TestControlAuditBudgetIsGlobalAndFinishReleasesHeadroom(t *testing.T) {
	controlAuditBackends(t, 2300, func(t *testing.T, b controlAuditBackend, _ func(time.Duration)) {
		ctx := context.Background()
		first := controlAuditSession(t, b, "app")
		second := controlAuditSession(t, b, "other")
		batch := make([]SeedValue, 6)
		for i := range batch {
			batch[i] = controlAuditValue("app", i)
		}
		if _, err := b.InstallSeeds(ctx, "app", first.Token, batch); err != nil {
			t.Fatal(err)
		}
		other := []SeedValue{controlAuditValue("other", 0), controlAuditValue("other", 1)}
		before := b.Usage()
		if n, err := b.InstallSeeds(ctx, "other", second.Token, other); n != 0 || !errors.Is(err, ErrCapacity) {
			t.Fatal("namespace escaped global budget", n, err)
		}
		if after := b.Usage(); after != before {
			t.Fatal(before, after)
		}
		if err := b.FinishSeeds(ctx, "app", first.Token); err != nil {
			t.Fatal(err)
		}
		if n, err := b.InstallSeeds(ctx, "other", second.Token, other); err != nil || n != 2 {
			t.Fatal("finish did not release control headroom", n, err)
		}
		if n, err := b.InstallSeeds(ctx, "app", first.Token, batch); err != nil || n != 0 {
			t.Fatal("finished capability became usable", n, err)
		}
	})
}

func TestControlAuditLongKeyCyclesReleaseEveryControlLedger(t *testing.T) {
	controlAuditBackends(t, 4096, func(t *testing.T, b controlAuditBackend, advance func(time.Duration)) {
		ctx := context.Background()
		for cycle := 0; cycle < 18; cycle++ {
			for _, namespace := range []string{"app", "other"} {
				s := controlAuditSession(t, b, namespace)
				batch := make([]SeedValue, 10)
				for i := range batch {
					batch[i] = controlAuditValue(namespace, cycle*10+i)
				}
				if _, err := b.InstallSeeds(ctx, namespace, s.Token, batch); err != nil {
					t.Fatal(cycle, err)
				}
				if u := b.Usage(); u.SeedSeenKeys > 20 || u.ControlBytes > u.ControlLimit || u.DataBytes > u.DataLimit {
					t.Fatal(u)
				}
			}
			var m *Memory
			if memory, ok := b.(*Memory); ok {
				m = memory
			} else {
				m = b.(*File).memory
			}
			for _, session := range m.seedSessions {
				if key := reflect.TypeOf(session.seen).Key(); key.Kind() != reflect.Array || key.Len() != 32 || key.Elem().Kind() != reflect.Uint8 {
					t.Fatal("seen ledger retains raw application IDs", key)
				}
			}
			switch cycle % 3 {
			case 0:
				for token, session := range m.seedSessions {
					if err := b.FinishSeeds(ctx, session.namespace, token); err != nil {
						t.Fatal(err)
					}
				}
			case 1:
				for _, ns := range []string{"app", "other"} {
					if _, err := b.MarkStale(ctx, ns, Selector{Kind: "key", Value: "absent"}, Never); err != nil {
						t.Fatal(err)
					}
				}
			case 2:
				advance(SeedSessionTTL + time.Nanosecond)
				// A new operation reclaims expired capabilities; Usage is a snapshot.
				ticket, err := b.ReserveSeeds(ctx, "cleanup")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := b.Expire(ctx, "cleanup", Selector{Kind: "key", Value: ticket.Token}); err != nil {
					t.Fatal(err)
				}
			}
			if u := b.Usage(); u.ControlBytes != 0 || u.SeedSeenKeys != 0 || u.SeedSessions != 0 || u.SeedTickets != 0 {
				t.Fatal("control ledger grew across completed cycles", cycle, u)
			}
		}
		if _, err := b.ReserveSeeds(ctx, "ticket-expiry"); err != nil {
			t.Fatal(err)
		}
		advance(SeedTicketTTL + time.Nanosecond)
		if _, err := b.ReserveSeeds(ctx, "app"); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Expire(ctx, "app", Selector{Kind: "key", Value: "absent"}); err != nil {
			t.Fatal(err)
		}
		if u := b.Usage(); u.ControlBytes != 0 {
			t.Fatal("ticket TTL retained control bytes", u)
		}
	})
}

func TestControlAuditAdmissionOwnsValueAndPolicyInputs(t *testing.T) {
	controlAuditBackends(t, 4096, func(t *testing.T, b controlAuditBackend, _ func(time.Duration)) {
		ctx := context.Background()
		s := controlAuditSession(t, b, "app")
		values := []SeedValue{controlAuditValue("app", 1)}
		id := values[0].ID
		p := Policy{FreshFor: time.Hour, ExpireAfter: 2 * time.Hour, Tags: []string{"original-tag"}, Paths: []string{"/original"}}
		if n, err := b.InstallSeeds(ctx, "app", s.Token, values); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		values[0].Value[0] = 'X'
		values[0].Policy.Tags[0], values[0].Policy.Paths[0] = "mutated", "/mutated"
		values[0].ID.Key = "mutated"
		v := controlAuditRead(t, b, id, p)
		if !v.Found || string(v.Value) != "original" {
			t.Fatal("caller mutated admitted data", v)
		}
		v.Value[0] = 'Y'
		if next := controlAuditRead(t, b, id, p); !next.Found || string(next.Value) != "original" {
			t.Fatal("reader mutated admitted data", next)
		}
		if n, err := b.Expire(ctx, "app", Selector{Kind: "tag", Value: "original-tag"}); err != nil || n != 1 {
			t.Fatal("caller mutated admitted index", n, err)
		}
	})
}

func TestControlAuditFileReplayReducedBudgetFailsClosed(t *testing.T) {
	ctx := context.Background()
	dir := fileAuditDir(t)
	opts := FileOptions{Capacity: 2, MaxBytes: 1 << 20, MaxDiskBytes: 32768, MaxControlBytes: 4096}
	f := openFileTest(t, dir, opts)
	tickets := make([]SeedTicket, 3)
	for i := range tickets {
		var err error
		tickets[i], err = f.ReserveSeeds(ctx, "app")
		if err != nil {
			t.Fatal(err)
		}
	}
	before := f.Usage()
	// Force journal turnover while all three acknowledged tickets are live.
	// Replaying a compacted snapshot must enforce the same control limit.
	for i := 0; i < 12; i++ {
		v := controlAuditValue("app", i)
		flight := acquire(t, f, v.ID, v.Policy)
		publish(t, f, v.ID, flight.Lease, bytes.Repeat([]byte{byte(i)}, 1800))
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	reduced := opts
	reduced.MaxControlBytes = before.ControlBytes - 1
	if reopened, err := OpenFile(dir, reduced); !errors.Is(err, ErrProtocol) {
		if reopened != nil {
			_ = reopened.Close()
		}
		t.Fatal("replay did not fail closed at lower control budget", err)
	}
	f = openFileTest(t, dir, opts)
	if u := f.Usage(); u.SeedTickets != 3 || u.ControlBytes != before.ControlBytes {
		t.Fatal(before, u)
	}
	session, err := f.BeginSeeds(ctx, "app", tickets[0].Token)
	if err != nil || session.Token == "" {
		t.Fatal(session, err)
	}
	value := controlAuditValue("app", 1)
	lease := acquire(t, f, value.ID, value.Policy)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	f = openFileTest(t, dir, opts)
	if u := f.Usage(); u.SeedSessions != 0 || u.SeedSeenKeys != 0 || u.SeedTickets != 2 {
		t.Fatal("ephemeral session replayed", u)
	}
	if old, err := f.BeginSeeds(ctx, "app", tickets[0].Token); err != nil || old.Token != "" {
		t.Fatal("consumed ticket replayed", old, err)
	}
	if n, err := f.InstallSeeds(ctx, "app", session.Token, []SeedValue{value}); err != nil || n != 0 {
		t.Fatal("old session admitted data after restart", n, err)
	}
	if committed, err := f.Commit(ctx, value.ID, lease.Lease, []byte("old lease")); err != nil || committed.Found {
		t.Fatal("old lease published after restart", committed, err)
	}
	if _, err := f.Expire(ctx, "app", Selector{Kind: "key", Value: "absent"}); err != nil {
		t.Fatal(err)
	}
	if u := f.Usage(); u.ControlBytes != 0 {
		t.Fatal(u)
	}
}

func TestControlAuditFileRejectedBeginTicketRemainsDurable(t *testing.T) {
	ctx := context.Background()
	dir := fileAuditDir(t)
	opts := FileOptions{MaxControlBytes: 600}
	f := openFileTest(t, dir, opts)
	ticket, err := f.ReserveSeeds(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if s, err := f.BeginSeeds(ctx, "app", ticket.Token); !errors.Is(err, ErrCapacity) || s.Token != "" {
		t.Fatal(s, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	opts.MaxControlBytes = 4096
	f = openFileTest(t, dir, opts)
	if s, err := f.BeginSeeds(ctx, "app", ticket.Token); err != nil || s.Token == "" {
		t.Fatal("failed admission durably consumed pending ticket", s, err)
	}
}
