package shared

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This measures canceled/missing loads' lease bookkeeping with a resident
// import ledger. It deliberately performs no journal writes in the timed loop;
// it is not storage throughput, total heap, RSS or an HTTP load benchmark.
func BenchmarkFileLeaseChurnWithSeedLedger(b *testing.B) {
	for _, count := range []int{0, 1000, 10000} {
		b.Run(fmt.Sprintf("seen=%d", count), func(b *testing.B) {
			dir := filepath.Join(b.TempDir(), "cache")
			if err := os.Mkdir(dir, 0700); err != nil {
				b.Fatal(err)
			}
			f, err := OpenFile(dir, FileOptions{Capacity: 64, MaxBytes: 1 << 20})
			if err != nil {
				b.Fatal(err)
			}
			defer f.Close()
			ctx := context.Background()
			origin := time.Now().Add(-time.Hour)
			f.now = func() time.Time { return origin }
			ticket, err := f.ReserveSeeds(ctx, "app")
			if err != nil {
				b.Fatal(err)
			}
			session, err := f.BeginSeeds(ctx, "app", ticket.Token)
			if err != nil {
				b.Fatal(err)
			}
			// Expired seeds keep their once-per-key evidence without retained values.
			now := origin.Add(time.Second)
			f.now = func() time.Time { return now }
			for start := 0; start < count; start += MaxSeedBatch {
				values := make([]SeedValue, 0, min(MaxSeedBatch, count-start))
				for i := start; i < min(start+MaxSeedBatch, count); i++ {
					values = append(values, SeedValue{ID: ID{"app", "v1", fmt.Sprintf("seed:%d", i)}, Policy: Policy{ExpireAfter: time.Nanosecond}, Value: []byte("null")})
				}
				if _, err := f.InstallSeeds(ctx, "app", session.Token, values); err != nil {
					b.Fatal(err)
				}
			}
			before := f.Usage()
			if before.SeedSeenKeys != count {
				b.Fatal(before)
			}
			id := ID{"app", "v1", "runtime-load"}
			policy := Policy{FreshFor: time.Second, ExpireAfter: time.Minute}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				v, err := f.Begin(ctx, id, policy, time.Second)
				if err != nil || v.Lease == "" {
					b.Fatal(v, err)
				}
				if err := f.Release(ctx, id, v.Lease); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			after := f.Usage()
			if after.SeedSeenKeys != count || after.ControlBytes != before.ControlBytes || after.Entries != 0 {
				b.Fatal(before, after)
			}
		})
	}
}
