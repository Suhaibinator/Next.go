package shared

import (
	"context"
	"testing"
	"time"
)

func TestEqualLifetimeIsExactTTL(t *testing.T) {
	m, advance := memory(t, 4, 1<<20)
	p := Policy{FreshFor: time.Second, ExpireAfter: time.Second}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	first := acquire(t, m, testID, p)
	publish(t, m, testID, first.Lease, []byte("old"))
	advance(time.Second)
	v, err := m.BeginStale(context.Background(), testID, p, time.Second)
	if err != nil || v.Found || v.Lease == "" {
		t.Fatal("TTL gained a stale interval", v, err)
	}
}
func TestScopeIdentityIsLogicalAndOpaque(t *testing.T) {
	a, _ := memory(t, 4, 1<<20)
	b, _ := memory(t, 4, 1<<20)
	s1, err := New(a, "private-tenant", "v1", Options{})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := New(b, "private-tenant", "v1", Options{Lease: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	s3, _ := New(a, "private-tenant", "v2", Options{})
	if len(s1.ScopeID()) != 64 || s1.ScopeID() != s2.ScopeID() || s1.ScopeID() == s3.ScopeID() {
		t.Fatal(s1.ScopeID(), s2.ScopeID(), s3.ScopeID())
	}
	if (*Store)(nil).ScopeID() != "" || (&Store{}).ScopeID() != "" {
		t.Fatal("unconstructed scope has identity")
	}
}
