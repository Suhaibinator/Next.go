package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/cache/shared"
)

func TestCommandControlQuotaPreservesDataAndActiveSession(t *testing.T) {
	for _, mode := range []string{"memory", "durable"} {
		t.Run(mode, func(t *testing.T) {
			args := []string{"-control-bytes", "1024", "-bytes", "4096", "-entries", "8"}
			if mode == "durable" {
				args = append(args, "-data-dir", durableDirectory(t))
			}
			run := startCacheCommand(t, args...)
			ctx := context.Background()
			id := shared.ID{Namespace: "app", Schema: "v1", Key: "retained"}
			policy := shared.Policy{FreshFor: time.Hour, ExpireAfter: 2 * time.Hour}
			commandPublish(t, run.backend, id, policy, "last good")
			ticket, err := run.backend.ReserveSeeds(ctx, "app")
			if err != nil {
				t.Fatal(err)
			}
			session, err := run.backend.BeginSeeds(ctx, "app", ticket.Token)
			if err != nil || session.Token == "" {
				t.Fatal(session, err)
			}
			if _, err = run.backend.ReserveSeeds(ctx, "app"); !errors.Is(err, shared.ErrCapacity) {
				t.Fatal("control quota did not reject admission alongside active session", err)
			}
			values := []shared.SeedValue{{ID: shared.ID{Namespace: "app", Schema: "v1", Key: "imported"}, Policy: policy, Value: []byte("1")}}
			if n, err := run.backend.InstallSeeds(ctx, "app", session.Token, values); err != nil || n != 1 {
				t.Fatal("failed admission discarded active authority", n, err)
			}
			if n, err := run.backend.InstallSeeds(ctx, "app", session.Token, values); err != nil || n != 0 {
				t.Fatal("failed admission discarded seen-key evidence", n, err)
			}
			view, err := run.backend.Begin(ctx, id, policy, time.Second)
			if err != nil || !view.Found || string(view.Value) != "last good" {
				t.Fatal("control exhaustion evicted ordinary data", view, err)
			}
			if err := run.backend.FinishSeeds(ctx, "app", session.Token); err != nil {
				t.Fatal(err)
			}
			if _, err := run.backend.ReserveSeeds(ctx, "app"); err != nil {
				t.Fatal("finished session did not release control budget", err)
			}
			run.stop(t)
		})
	}
}

func TestCommandControlQuotaRetainsTicketAcrossFailedBeginAndRestart(t *testing.T) {
	dir := durableDirectory(t)
	args := []string{"-data-dir", dir, "-control-bytes", "600"}
	first := startCacheCommand(t, args...)
	ctx := context.Background()
	id := shared.ID{Namespace: "app", Schema: "v1", Key: "persisted"}
	policy := shared.Policy{FreshFor: time.Hour, ExpireAfter: 2 * time.Hour}
	commandPublish(t, first.backend, id, policy, "retained")
	ticket, err := first.backend.ReserveSeeds(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := first.backend.BeginSeeds(ctx, "app", ticket.Token); !errors.Is(err, shared.ErrCapacity) {
			t.Fatal("insufficient budget consumed pending ticket", err)
		}
	}
	first.stop(t)
	// Recovery cannot silently drop retained authority to honor a smaller quota.
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := execute(bounded, []string{"-addr", "127.0.0.1:0", "-data-dir", dir, "-control-bytes", "514"}, commandTestToken, io.Discard); !errors.Is(err, shared.ErrProtocol) {
		t.Fatal("recovery accepted insufficient control quota", err)
	}
	second := startCacheCommand(t, "-data-dir", dir, "-control-bytes", "1024")
	session, err := second.backend.BeginSeeds(ctx, "app", ticket.Token)
	if err != nil || session.Token == "" {
		t.Fatal("failed admission/recovery lost pending ticket", session, err)
	}
	view, err := second.backend.Begin(ctx, id, policy, time.Second)
	if err != nil || !view.Found || string(view.Value) != "retained" {
		t.Fatal(view, err)
	}
	if err := second.backend.FinishSeeds(ctx, "app", session.Token); err != nil {
		t.Fatal(err)
	}
	second.stop(t)
}

func TestCommandControlFlagsValidateBeforeStorageAndPreserveMode(t *testing.T) {
	for _, value := range []string{"0", "-1", strconv.FormatInt((1<<30)+1, 10)} {
		for _, durable := range []bool{false, true} {
			dir := filepath.Join(t.TempDir(), "cache")
			args := []string{"-control-bytes", value}
			if durable {
				args = append(args, "-data-dir", dir)
			}
			if err := execute(context.Background(), args, commandTestToken, io.Discard); err == nil {
				t.Fatal("invalid control budget accepted", value, durable)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("invalid flags created storage", err)
			}
		}
	}
	for _, args := range [][]string{nil, {"-control-bytes", strconv.FormatInt(shared.DefaultControlBytes, 10)}} {
		run := startCacheCommand(t, args...)
		if !strings.Contains(run.status.String(), "memory only") || strings.Contains(run.status.String(), "durable file storage") {
			t.Fatal(run.status.String())
		}
		run.stop(t)
	}
}
