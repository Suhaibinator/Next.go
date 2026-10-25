package shared

import (
	"strings"
	"testing"
)

func TestNamespaceIdentityMatchesInvalidationRealmAcrossSchemas(t *testing.T) {
	firstBackend, _ := memory(t, 4, 1<<20)
	secondBackend, _ := memory(t, 4, 1<<20)
	first, err := New(firstBackend, "private-deployment-namespace", "schema-a", Options{})
	if err != nil {
		t.Fatal(err)
	}
	changedSchema, err := New(firstBackend, "private-deployment-namespace", "schema-b", Options{})
	if err != nil {
		t.Fatal(err)
	}
	changedBackend, err := New(secondBackend, "private-deployment-namespace", "schema-a", Options{})
	if err != nil {
		t.Fatal(err)
	}
	other, err := New(firstBackend, "other-deployment-namespace", "schema-a", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.NamespaceIdentity()) != 64 || strings.Contains(first.NamespaceIdentity(), "private") || first.NamespaceIdentity() != changedSchema.NamespaceIdentity() || first.NamespaceIdentity() != changedBackend.NamespaceIdentity() || first.NamespaceIdentity() == other.NamespaceIdentity() {
		t.Fatal("namespace identity did not match logical invalidation realm")
	}
	if first.ScopeID() == changedSchema.ScopeID() {
		t.Fatal("schema remains a distinct value serialization scope")
	}
	for _, invalid := range []*Store{nil, {}, {backend: firstBackend, namespace: "bad namespace", schema: "schema"}} {
		if invalid.NamespaceIdentity() != "" {
			t.Fatal("unconstructed/invalid store has identity")
		}
	}
}
