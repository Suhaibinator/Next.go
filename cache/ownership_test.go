package cache

import (
	"context"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestRetainedKeysAndLabelsOwnTheirStringBacking(t *testing.T) {
	for _, bootstrap := range []bool{false, true} {
		t.Run(map[bool]string{false: "load", true: "bootstrap"}[bootstrap], func(t *testing.T) {
			parts := []string{"key", "tag", "/short?discard=1"}
			blob := strings.Join(parts, "|") + strings.Repeat("x", 4<<20)
			offset := 0
			borrowed := make([]string, len(parts))
			for i, p := range parts {
				borrowed[i] = blob[offset : offset+len(p)]
				offset += len(p) + 1
			}
			base := uintptr(unsafe.Pointer(unsafe.StringData(blob)))
			limit := base + uintptr(len(blob))
			owns := func(s string) {
				t.Helper()
				if len(s) > 0 {
					at := uintptr(unsafe.Pointer(unsafe.StringData(s)))
					if at >= base && at < limit {
						t.Fatalf("retained %q aliases large input", s)
					}
				}
			}
			store := New(2, WithMaxBytes(4096, func(any) int64 { return 1 }))
			tags := make([]string, 1024)
			for i := range tags {
				tags[i] = borrowed[1]
			}
			options := Options{TTL: time.Minute, Tags: tags, Paths: []string{borrowed[2]}}
			if bootstrap {
				if n, err := Bootstrap(context.Background(), store, []Seed[int]{{Key: borrowed[0], Value: 7, LoadedAt: time.Now(), Options: options}}); err != nil || n != 1 {
					t.Fatal(n, err)
				}
			} else {
				if _, err := GetOrLoad(context.Background(), store, borrowed[0], options, func(context.Context) (int, error) { return 7, nil }); err != nil {
					t.Fatal(err)
				}
			}
			for key, elem := range store.entries {
				owns(key)
				e := elem.Value.(*entry)
				owns(e.key)
				for tag := range e.tags {
					owns(tag)
				}
				for path := range e.paths {
					owns(path)
				}
				if len(e.tags) != 1 || len(e.paths) != 1 {
					t.Fatal("labels not deduplicated")
				}
			}
			if store.ExpirePath("/short") != 1 {
				t.Fatal("copied path changed matching")
			}
		})
	}
}
