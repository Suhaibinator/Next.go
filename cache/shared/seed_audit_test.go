package shared

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

type seedAuditBackend interface {
	Backend
	SeedBackend
}

var seedAuditPolicy = Policy{FreshFor: time.Hour, ExpireAfter: 2 * time.Hour, Tags: []string{"seed-audit"}, Paths: []string{"/seed-audit"}}

func seedAuditValue(key, value string) SeedValue {
	return SeedValue{ID: ID{Namespace: "app", Schema: "seed-audit-v1", Key: key}, Policy: seedAuditPolicy, Value: []byte(value)}
}
func seedAuditSession(t *testing.T, b SeedBackend) (SeedTicket, SeedSession) {
	t.Helper()
	ticket, err := b.ReserveSeeds(context.Background(), "app")
	if err != nil || !validToken(ticket.Token) || ticket.CreatedAt.IsZero() {
		t.Fatal(ticket, err)
	}
	session, err := b.BeginSeeds(context.Background(), "app", ticket.Token)
	if err != nil || !validToken(session.Token) || session.LeaseFor <= 0 || session.LeaseFor > 5*time.Minute {
		t.Fatal(session, err)
	}
	return ticket, session
}
func seedAuditBackends(t *testing.T, run func(*testing.T, seedAuditBackend)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { m, _ := memory(t, 8, 1<<20); run(t, m) })
	t.Run("file", func(t *testing.T) {
		run(t, openFileTest(t, fileAuditDir(t), FileOptions{Capacity: 8, MaxDiskBytes: 1 << 20}))
	})
}

func TestSeedAuditZeroMatchInvalidationFencesCapabilitiesButAllowsLaterBuilds(t *testing.T) {
	for _, mode := range []string{"expire", "mark-never", "mark-zero"} {
		t.Run(mode, func(t *testing.T) {
			seedAuditBackends(t, func(t *testing.T, b seedAuditBackend) {
				ctx := context.Background()
				pending, err := b.ReserveSeeds(ctx, "app")
				if err != nil {
					t.Fatal(err)
				}
				_, session := seedAuditSession(t, b)
				selector := Selector{Kind: "key", Value: "not-yet-present"}
				n := 0
				switch mode {
				case "expire":
					n, err = b.Expire(ctx, "app", selector)
				case "mark-never":
					n, err = b.MarkStale(ctx, "app", selector, Never)
				case "mark-zero":
					n, err = b.MarkStale(ctx, "app", selector, 0)
				}
				if err != nil || n != 0 {
					t.Fatal(n, err)
				}
				if old, err := b.BeginSeeds(ctx, "app", pending.Token); err != nil || old.Token != "" {
					t.Fatal("zero-match invalidation retained a pre-write build ticket", old, err)
				}
				value := seedAuditValue("alpha", "old-build")
				if n, err := b.InstallSeeds(ctx, "app", session.Token, []SeedValue{value}); err != nil || n != 0 {
					t.Fatal("zero-match invalidation retained an import session", n, err)
				}
				_, fresh := seedAuditSession(t, b)
				value.Value = []byte("new-build")
				if n, err := b.InstallSeeds(ctx, "app", fresh.Token, []SeedValue{value}); err != nil || n != 1 {
					t.Fatal("invalidation permanently disabled later build imports", n, err)
				}
				view, err := b.Begin(ctx, value.ID, value.Policy, time.Second)
				if err != nil || !view.Found || string(view.Value) != "new-build" {
					t.Fatal(view, err)
				}
			})
		})
	}
}

func TestSeedAuditOriginalAuthorityAgeAndExpiredAttempts(t *testing.T) {
	m, advance := memory(t, 8, 1<<20)
	ctx := context.Background()
	ticket, err := m.ReserveSeeds(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	advance(2 * time.Minute)
	session, err := m.BeginSeeds(ctx, "app", ticket.Token)
	if err != nil || session.Token == "" {
		t.Fatal(session, err)
	}
	value := seedAuditValue("aged", "old")
	value.Policy = Policy{FreshFor: time.Minute, ExpireAfter: 3 * time.Minute}
	if n, err := m.InstallSeeds(ctx, "app", session.Token, []SeedValue{value}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	view := acquire(t, m, value.ID, value.Policy)
	if !view.Found || view.Age != 2*time.Minute || view.FreshFor != 0 || view.ExpiresIn != time.Minute {
		t.Fatal("install reset the authority build age", view)
	}
	if err := m.Release(ctx, value.ID, view.Lease); err != nil {
		t.Fatal(err)
	}
	expired := seedAuditValue("expired-at-install", "expired")
	expired.Policy = Policy{FreshFor: 0, ExpireAfter: time.Minute}
	if n, err := m.InstallSeeds(ctx, "app", session.Token, []SeedValue{expired}); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	expired.Policy = seedAuditPolicy
	if n, err := m.InstallSeeds(ctx, "app", session.Token, []SeedValue{expired}); err != nil || n != 0 {
		t.Fatal("skipped expired key was imported again with a changed lifetime", n, err)
	}
	advance(time.Minute + time.Nanosecond)
	if n, err := m.InstallSeeds(ctx, "app", session.Token, []SeedValue{value}); err != nil || n != 0 {
		t.Fatal("expired original generation was reimported", n, err)
	}
	probe := acquire(t, m, value.ID, value.Policy)
	if probe.Found {
		t.Fatal(probe)
	}
}

func TestSeedAuditSeenSetProtectsRuntimeFlightsAndEvictedSeeds(t *testing.T) {
	m, _ := memory(t, 1, 1<<20)
	ctx := context.Background()
	_, session := seedAuditSession(t, m)
	alpha := seedAuditValue("alpha", "old-seed")
	flight := acquire(t, m, alpha.ID, alpha.Policy)
	if n, err := m.InstallSeeds(ctx, "app", session.Token, []SeedValue{alpha}); err != nil || n != 0 {
		t.Fatal("live loader was overwritten", n, err)
	}
	if err := m.Release(ctx, alpha.ID, flight.Lease); err != nil {
		t.Fatal(err)
	}
	if n, err := m.InstallSeeds(ctx, "app", session.Token, []SeedValue{alpha}); err != nil || n != 0 {
		t.Fatal("retry resurrected a skipped runtime-owned key", n, err)
	}
	beta := seedAuditValue("beta", "seed-beta")
	if n, err := m.InstallSeeds(ctx, "app", session.Token, []SeedValue{beta}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	gamma := seedAuditValue("gamma", "")
	eviction := acquire(t, m, gamma.ID, gamma.Policy)
	if err := m.Release(ctx, gamma.ID, eviction.Lease); err != nil {
		t.Fatal(err)
	}
	if n, err := m.InstallSeeds(ctx, "app", session.Token, []SeedValue{beta}); err != nil || n != 0 {
		t.Fatal("evicted seed was reimported by retry", n, err)
	}
	probe := acquire(t, m, beta.ID, beta.Policy)
	if probe.Found {
		t.Fatal(probe)
	}
}

func TestSeedAuditBatchValidationAndGlobalCapabilityBounds(t *testing.T) {
	m, _ := memory(t, 8, 1<<20)
	ctx := context.Background()
	_, session := seedAuditSession(t, m)
	value := seedAuditValue("alpha", "value")
	if _, err := m.InstallSeeds(ctx, "app", session.Token, []SeedValue{value, value}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if n, err := m.InstallSeeds(ctx, "app", session.Token, []SeedValue{value}); err != nil || n != 1 {
		t.Fatal("rejected batch mutated the session seen set", n, err)
	}
	if err := m.FinishSeeds(ctx, "app", session.Token); err != nil {
		t.Fatal(err)
	}
	if n, err := m.InstallSeeds(ctx, "app", session.Token, []SeedValue{seedAuditValue("beta", "late")}); err != nil || n != 0 {
		t.Fatal("finished session imported more values", n, err)
	}
	tickets, advance := memory(t, 1, 1024)
	var first SeedTicket
	for i := 0; i < 128; i++ {
		ticket, err := tickets.ReserveSeeds(ctx, fmt.Sprintf("namespace-%d", i))
		if err != nil {
			t.Fatal(i, err)
		}
		if i == 0 {
			first = ticket
		}
		advance(time.Nanosecond)
	}
	newest, err := tickets.ReserveSeeds(ctx, "one-more")
	if err != nil || newest.Token == "" || len(tickets.seedTickets) > MaxSeedTickets {
		t.Fatal("ticket metadata was not bounded by oldest-ticket eviction", newest, err, len(tickets.seedTickets))
	}
	if old, err := tickets.BeginSeeds(ctx, "namespace-0", first.Token); err != nil || old.Token != "" {
		t.Fatal("evicted oldest ticket retained authority", old, err)
	}
	if session, err := tickets.BeginSeeds(ctx, "one-more", newest.Token); err != nil || session.Token == "" {
		t.Fatal("new build lost authority at ticket capacity", session, err)
	}
	expiring, err := tickets.ReserveSeeds(ctx, "expiry")
	if err != nil {
		t.Fatal(err)
	}
	advance(31 * 24 * time.Hour)
	if old, err := tickets.BeginSeeds(ctx, "expiry", expiring.Token); err != nil || old.Token != "" {
		t.Fatal(old, err)
	}
	if _, err := tickets.ReserveSeeds(ctx, "new-build"); err != nil {
		t.Fatal("expired ticket capacity was not reclaimed", err)
	}
	sessions, _ := memory(t, 1, 1024)
	var active SeedSession
	for i := 0; i < 16; i++ {
		namespace := fmt.Sprintf("session-%d", i)
		ticket, err := sessions.ReserveSeeds(ctx, namespace)
		if err != nil {
			t.Fatal(err)
		}
		s, err := sessions.BeginSeeds(ctx, namespace, ticket.Token)
		if err != nil || s.Token == "" {
			t.Fatal(i, s, err)
		}
		if i == 0 {
			active = s
		}
	}
	waiting, err := sessions.ReserveSeeds(ctx, "waiting")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.BeginSeeds(ctx, "waiting", waiting.Token); !errors.Is(err, ErrCapacity) {
		t.Fatal("session bound was not global", err)
	}
	if err := sessions.FinishSeeds(ctx, "session-0", active.Token); err != nil {
		t.Fatal(err)
	}
	if s, err := sessions.BeginSeeds(ctx, "waiting", waiting.Token); err != nil || s.Token == "" {
		t.Fatal("capacity rejection consumed the build ticket", s, err)
	}
}

func TestSeedAuditFileConsumptionAndSeenSetSurviveCompaction(t *testing.T) {
	dir := fileAuditDir(t)
	f := openFileTest(t, dir, FileOptions{Capacity: 1, MaxDiskBytes: 8192})
	ctx := context.Background()
	ticket, session := seedAuditSession(t, f)
	alpha := seedAuditValue("alpha", "build-alpha")
	if n, err := f.InstallSeeds(ctx, "app", session.Token, []SeedValue{alpha}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	beta := seedAuditValue("runtime-beta", "")
	beta.Policy = Policy{FreshFor: 0, ExpireAfter: Never}
	compacted := false
	previous := f.size
	for i := 0; i < 20; i++ {
		lease := acquire(t, f, beta.ID, beta.Policy)
		publish(t, f, beta.ID, lease.Lease, bytes.Repeat([]byte{byte(i)}, 700))
		if f.size < previous {
			compacted = true
		}
		previous = f.size
	}
	if !compacted {
		t.Fatal("fixture did not exercise bounded WAL compaction")
	}
	if n, err := f.InstallSeeds(ctx, "app", session.Token, []SeedValue{alpha}); err != nil || n != 0 {
		t.Fatal("compaction lost live-session eviction history", n, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	f = openFileTest(t, dir, FileOptions{Capacity: 1, MaxDiskBytes: 8192})
	if old, err := f.BeginSeeds(ctx, "app", ticket.Token); err != nil || old.Token != "" {
		t.Fatal("consumed build ticket restored by recovery", old, err)
	}
	if n, err := f.InstallSeeds(ctx, "app", session.Token, []SeedValue{alpha}); err != nil || n != 0 {
		t.Fatal("ephemeral session survived restart", n, err)
	}
	view := acquire(t, f, beta.ID, beta.Policy)
	if !view.Found || len(view.Value) != 700 || view.Value[0] != 19 {
		t.Fatal("seed metadata compaction lost runtime value", view)
	}
	if err := f.Release(ctx, beta.ID, view.Lease); err != nil {
		t.Fatal(err)
	}
	missing := acquire(t, f, alpha.ID, alpha.Policy)
	if missing.Found {
		t.Fatal("old build seed reappeared after recovery", missing)
	}
}

func TestSeedAuditFileUnknownAcknowledgmentsCannotReimport(t *testing.T) {
	for _, mode := range []string{"begin", "install"} {
		t.Run(mode, func(t *testing.T) {
			dir := fileAuditDir(t)
			f := openFileTest(t, dir, FileOptions{})
			ctx := context.Background()
			ticket, err := f.ReserveSeeds(ctx, "app")
			if err != nil {
				t.Fatal(err)
			}
			var session SeedSession
			if mode == "install" {
				session, err = f.BeginSeeds(ctx, "app", ticket.Token)
				if err != nil || session.Token == "" {
					t.Fatal(session, err)
				}
			}
			work, cancel := context.WithCancel(ctx)
			defer cancel()
			f.syncFile = func(file *os.File) error {
				err := file.Sync()
				if err == nil {
					cancel()
				}
				return err
			}
			value := seedAuditValue("alpha", "pre-write-build")
			if mode == "begin" {
				_, err = f.BeginSeeds(work, "app", ticket.Token)
			} else {
				_, err = f.InstallSeeds(work, "app", session.Token, []SeedValue{value})
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("fixture did not lose the acknowledgment after sync", err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			f = openFileTest(t, dir, FileOptions{})
			if _, err := f.Expire(ctx, "app", Selector{Kind: "tag", Value: "seed-audit"}); err != nil {
				t.Fatal(err)
			}
			if old, err := f.BeginSeeds(ctx, "app", ticket.Token); err != nil || old.Token != "" {
				t.Fatal("unknown consumption ACK reused a build ticket", old, err)
			}
			if session.Token != "" {
				if n, err := f.InstallSeeds(ctx, "app", session.Token, []SeedValue{value}); err != nil || n != 0 {
					t.Fatal("unknown import ACK resurrected an invalidated seed", n, err)
				}
			}
			_, fresh := seedAuditSession(t, f)
			value.Value = []byte("fresh-build")
			if n, err := f.InstallSeeds(ctx, "app", fresh.Token, []SeedValue{value}); err != nil || n != 1 {
				t.Fatal("later post-invalidation build was blocked", n, err)
			}
		})
	}
}
