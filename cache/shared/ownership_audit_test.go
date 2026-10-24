package shared

import (
	"context"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// These checks inspect immutable backing addresses only. No unsafe mutation,
// garbage-collector timing, or process-memory measurement is involved.
type ownershipAuditInput struct {
	blob   string
	id     ID
	policy Policy
}

type ownershipAuditBackend interface {
	Backend
	SeedBackend
	Usage() Usage
}

func ownershipAuditInputs(t *testing.T) ownershipAuditInput {
	t.Helper()
	const prefix = "tenant|schema|cms-key|header-tag|"
	blob := prefix + "/owned/path?query=" + strings.Repeat("x", 4<<20)
	parts := strings.SplitN(blob, "|", 5)
	u, err := url.ParseRequestURI(parts[4])
	if err != nil {
		t.Fatal(err)
	}
	header := http.Header{"X-Cache-Tag": []string{parts[3]}}
	in := ownershipAuditInput{blob: blob, id: ID{Namespace: parts[0], Schema: parts[1], Key: parts[2]}, policy: Policy{FreshFor: time.Hour, ExpireAfter: 2 * time.Hour, Tags: []string{header.Get("X-Cache-Tag")}, Paths: []string{u.Path}}}
	for _, value := range []string{in.id.Namespace, in.id.Schema, in.id.Key, in.policy.Tags[0], in.policy.Paths[0]} {
		if !ownershipAuditInside(value, blob) {
			t.Fatal("fixture did not create an aliased input")
		}
	}
	return in
}

func ownershipAuditInside(value, blob string) bool {
	if value == "" || blob == "" {
		return false
	}
	start := uintptr(unsafe.Pointer(unsafe.StringData(blob)))
	address := uintptr(unsafe.Pointer(unsafe.StringData(value)))
	inside := address >= start && address-start < uintptr(len(blob))
	runtime.KeepAlive(value)
	runtime.KeepAlive(blob)
	return inside
}

func ownershipAuditOutside(t *testing.T, name, value, blob string) {
	t.Helper()
	if ownershipAuditInside(value, blob) {
		t.Fatalf("retained %s pins a multi-megabyte caller buffer", name)
	}
}

func ownershipAuditState(t *testing.T, m *Memory, blob string) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	checkID := func(name string, id ID) {
		ownershipAuditOutside(t, name+" namespace", id.Namespace, blob)
		ownershipAuditOutside(t, name+" schema", id.Schema, blob)
		ownershipAuditOutside(t, name+" key", id.Key, blob)
	}
	for id, element := range m.entries {
		checkID("map ID", id)
		entry := element.Value.(*memoryEntry)
		checkID("entry ID", entry.id)
		for _, label := range entry.policy.Tags {
			ownershipAuditOutside(t, "policy tag", label, blob)
		}
		for _, label := range entry.policy.Paths {
			ownershipAuditOutside(t, "policy path", label, blob)
		}
	}
	for _, ticket := range m.seedTickets {
		ownershipAuditOutside(t, "pending namespace", ticket.namespace, blob)
	}
	for _, session := range m.seedSessions {
		ownershipAuditOutside(t, "active namespace", session.namespace, blob)
	}
}

func ownershipAuditBackends(t *testing.T, run func(*testing.T, ownershipAuditBackend, func() *Memory)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		m, err := NewMemory(4, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		run(t, m, func() *Memory { return m })
	})
	t.Run("file", func(t *testing.T) {
		f := openFileTest(t, fileAuditDir(t), FileOptions{Capacity: 4, MaxBytes: 1 << 20, MaxDiskBytes: 1 << 20})
		run(t, f, func() *Memory { return f.memory })
	})
}

func TestOwnershipAuditOrdinaryAdmissionOwnsShortStringsFromLargeRequestBuffer(t *testing.T) {
	ownershipAuditBackends(t, func(t *testing.T, b ownershipAuditBackend, memory func() *Memory) {
		in := ownershipAuditInputs(t)
		ctx := context.Background()
		flight, err := b.Begin(ctx, in.id, in.policy, time.Second)
		if err != nil || flight.Lease == "" {
			t.Fatal(flight, err)
		}
		ownershipAuditState(t, memory(), in.blob)
		committed, err := b.Commit(ctx, in.id, flight.Lease, []byte("meaningful content"))
		if err != nil || !committed.Found || string(committed.Value) != "meaningful content" {
			t.Fatal(committed, err)
		}
		ownershipAuditState(t, memory(), in.blob)
		if u := b.Usage(); u.Entries != 1 || u.DataBytes > 4096 || u.ControlBytes != 0 {
			t.Fatal("short logical input changed accounted budgets", u)
		}
		view, err := b.Begin(ctx, ID{Namespace: "tenant", Schema: "schema", Key: "cms-key"}, Policy{FreshFor: time.Hour, ExpireAfter: 2 * time.Hour, Tags: []string{"header-tag"}, Paths: []string{"/owned/path"}}, time.Second)
		if err != nil || !view.Found || string(view.Value) != "meaningful content" {
			t.Fatal("ownership changed logical identity", view, err)
		}
		if n, err := b.Expire(ctx, "tenant", Selector{Kind: "path", Value: "/owned/path"}); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		runtime.KeepAlive(in.blob)
	})
}

func TestOwnershipAuditSeedCapabilitiesAndInstalledLabelsOwnTheirBacking(t *testing.T) {
	ownershipAuditBackends(t, func(t *testing.T, b ownershipAuditBackend, memory func() *Memory) {
		in := ownershipAuditInputs(t)
		ctx := context.Background()
		ticket, err := b.ReserveSeeds(ctx, in.id.Namespace)
		if err != nil {
			t.Fatal(err)
		}
		if u := b.Usage(); u.SeedTickets != 1 {
			t.Fatal("reservation was not retained", u)
		}
		ownershipAuditState(t, memory(), in.blob)
		session, err := b.BeginSeeds(ctx, in.id.Namespace, ticket.Token)
		if err != nil || session.Token == "" {
			t.Fatal(session, err)
		}
		if u := b.Usage(); u.SeedTickets != 0 || u.SeedSessions != 1 {
			t.Fatal("session was not retained", u)
		}
		ownershipAuditState(t, memory(), in.blob)
		if n, err := b.InstallSeeds(ctx, in.id.Namespace, session.Token, []SeedValue{{ID: in.id, Policy: in.policy, Value: []byte("build content")}}); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		ownershipAuditState(t, memory(), in.blob)
		if u := b.Usage(); u.Entries != 1 || u.SeedSessions != 1 || u.SeedSeenKeys != 1 || u.DataBytes > 4096 || u.ControlBytes > 1024 {
			t.Fatal("ownership changed logical quotas", u)
		}
		view, err := b.Begin(ctx, ID{Namespace: "tenant", Schema: "schema", Key: "cms-key"}, Policy{FreshFor: time.Hour, ExpireAfter: 2 * time.Hour, Tags: []string{"header-tag"}, Paths: []string{"/owned/path"}}, time.Second)
		if err != nil || !view.Found || string(view.Value) != "build content" {
			t.Fatal(view, err)
		}
		if n, err := b.Expire(ctx, "tenant", Selector{Kind: "tag", Value: "header-tag"}); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		runtime.KeepAlive(in.blob)
	})
}
