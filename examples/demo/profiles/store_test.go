//go:build !js

package profiles

import (
	"context"
	"testing"
)

func TestProfileStoreValidatesVersionsCancellationAndCanonicalValues(t *testing.T) {
	const id = "native-store-profile"
	t.Cleanup(func() { store.Lock(); delete(store.records, id); store.Unlock() })
	initial, err := Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	input := initial
	input.Name = "  Grace Hopper  "
	input.Email = "GRACE@example.test"
	input.Newsletter = true
	input.Role = "editor"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Save(ctx, input); err == nil {
		t.Fatal("canceled write")
	}
	saved, fields, err := Save(context.Background(), input)
	if err != nil || len(fields) != 0 || saved.Name != "Grace Hopper" || saved.Email != "grace@example.test" || saved.Version != "1" {
		t.Fatal(saved, fields, err)
	}
	if _, fields, err := Save(context.Background(), input); err != nil || fields["version"] == "" {
		t.Fatal("stale save accepted", fields, err)
	}
	invalid := saved
	invalid.Email = "blocked@example.test"
	if _, fields, err := Save(context.Background(), invalid); err != nil || fields["email"] == "" {
		t.Fatal(fields, err)
	}
	actual, _ := Get(context.Background(), id)
	if actual != saved {
		t.Fatal("rejected input mutated record")
	}
	merged := Merge(Profile{Name: "newer", Email: input.Email, Role: input.Role, Newsletter: true}, input, saved)
	if merged.Name != "newer" || merged.Version != "1" || merged.Email != "grace@example.test" {
		t.Fatal(merged)
	}
}
