//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"strings"
	"syscall/js"
	"testing"
)

func TestOfflineStatusConflictGuardsKeepExactSentinelAndRejectBeforeMutation(t *testing.T) {
	cases := []struct {
		name, reason string
		configure    func(*offlineTestFixture)
	}{
		{"list-shape", "registration-list", func(f *offlineTestFixture) {
			f.sw.Set("getRegistrations", f.fn(func(js.Value, []js.Value) any { return f.resolve("not-a-registration-list") }))
		}},
		{"list-bound", "registration-list", func(f *offlineTestFixture) {
			f.sw.Set("getRegistrations", f.fn(func(js.Value, []js.Value) any { return f.resolve(js.Global().Get("Array").New(65)) }))
		}},
		{"scope-type", "registration-scope-type", func(f *offlineTestFixture) { f.registration.Set("scope", 42) }},
		{"overlapping-scope", "registration-scope", func(f *offlineTestFixture) { f.registration.Set("scope", f.c.descriptor.Scope+"/child") }},
		{"foreign-active", "registration-active-worker", func(f *offlineTestFixture) {
			f.registration.Set("active", js.ValueOf(map[string]any{"scriptURL": "https://reader.test/foreign-worker.js"}))
		}},
		{"foreign-waiting", "registration-waiting-worker", func(f *offlineTestFixture) {
			f.registration.Set("waiting", js.ValueOf(map[string]any{"scriptURL": "https://reader.test/foreign-worker.js"}))
		}},
		{"foreign-installing", "registration-installing-worker", func(f *offlineTestFixture) {
			f.registration.Set("installing", js.ValueOf(map[string]any{"scriptURL": "https://reader.test/foreign-worker.js"}))
		}},
		{"duplicate-overlap", "registration-duplicate", func(f *offlineTestFixture) { f.registrations = []js.Value{f.registration, f.registration} }},
		{"foreign-controller", "controller-worker", func(f *offlineTestFixture) {
			f.sw.Set("controller", js.ValueOf(map[string]any{"scriptURL": "https://reader.test/foreign-worker.js"}))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := offlineBrowserFixture(t)
			tc.configure(f)
			if err := RefreshOfflineStatus(context.Background()); err != ErrOfflineConflict {
				t.Fatalf("ownership rejection changed exact error identity: %v", err)
			}
			state := OfflineStatus()
			if f.c.conflictReason != tc.reason || state.Phase != "error" || state.LastError != ErrOfflineConflict.Error()+" ["+tc.reason+"]" {
				t.Fatalf("wrong public guard diagnostic: reason=%q state=%+v", f.c.conflictReason, state)
			}
			if f.c.busy || len(f.commands) != 0 || f.registers != 0 || f.updates != 0 || f.unregisters != 0 {
				t.Fatal("rejected status leaked admission, sent a message or mutated a registration")
			}
			f.balanced(t)
		})
	}
}

func TestOfflineStatusConflictIdentifiesChangedMessageTargetWithoutExtraGetterReads(t *testing.T) {
	for _, target := range []string{"active", "waiting"} {
		t.Run(target, func(t *testing.T) {
			f := offlineBrowserFixture(t)
			stats := js.ValueOf(map[string]any{"reads": 0})
			foreign := js.ValueOf(map[string]any{"scriptURL": "https://reader.test/foreign-worker.js"})
			// Validation sees the owned worker. Selection later sees a foreign
			// target, modeling a changed browser property between observations.
			js.Global().Get("Function").New("registration", "target", "stats", "owned", "foreign", `
Object.defineProperty(registration, target, {configurable:true, get(){stats.reads++;return stats.reads===1?owned:foreign;}});
`).Invoke(f.registration, target, stats, f.worker, foreign)
			if err := RefreshOfflineStatus(context.Background()); err != ErrOfflineConflict {
				t.Fatalf("changed message target was accepted or wrapped: %v", err)
			}
			if f.c.conflictReason != "message-worker" || OfflineStatus().LastError != ErrOfflineConflict.Error()+" [message-worker]" || stats.Get("reads").Int() != 2 {
				t.Fatalf("wrong target guard/read count: reason=%q reads=%v state=%+v", f.c.conflictReason, stats.Get("reads"), OfflineStatus())
			}
			wantMessages := 0
			if target == "waiting" {
				wantMessages = 1 // The unchanged active worker's status precedes waiting.
			}
			if len(f.commands) != wantMessages || f.registers != 0 || f.updates != 0 || f.unregisters != 0 || f.c.busy {
				t.Fatal("changed target caused extra messages, mutation or held admission")
			}
			for _, command := range f.commands {
				if command.Command != "status" {
					t.Fatal("status observation sent a mutating command")
				}
			}
			f.balanced(t)
		})
	}
}

func TestOfflineEnableConflictKeepsActiveWorkerIdentityFence(t *testing.T) {
	f := offlineBrowserFixture(t)
	stats := js.ValueOf(map[string]any{"reads": 0})
	foreign := js.ValueOf(map[string]any{"scriptURL": f.c.descriptor.WorkerURL})
	js.Global().Get("Function").New("registration", "stats", "owned", "foreign", `
Object.defineProperty(registration, "active", {configurable:true, get(){stats.reads++;return stats.reads<=2?owned:foreign;}});
`).Invoke(f.registration, stats, f.worker, foreign)
	if err := EnableOffline(context.Background()); err != ErrOfflineConflict {
		t.Fatalf("changed active identity was accepted or wrapped: %v", err)
	}
	if f.c.conflictReason != "active-worker-changed" || OfflineStatus().LastError != ErrOfflineConflict.Error()+" [active-worker-changed]" || stats.Get("reads").Int() != 4 {
		t.Fatalf("active identity fence diagnostic/read count changed: %q %+v", f.c.conflictReason, OfflineStatus())
	}
	if len(f.commands) != 1 || f.commands[0].Command != "status" || f.registers != 0 || f.updates != 0 || f.unregisters != 0 || f.c.busy {
		t.Fatal("changed active worker received a mutation or retained admission")
	}
	f.balanced(t)
}

func TestOfflineInstallationConflictRejectsForeignWorkerBeforeListening(t *testing.T) {
	f := offlineBrowserFixture(t)
	f.registration.Set("installing", js.ValueOf(map[string]any{"scriptURL": "https://reader.test/foreign-worker.js"}))
	if err := f.c.waitInstallation(context.Background(), f.registration); err != ErrOfflineConflict || f.c.conflictReason != "worker-state-target" {
		t.Fatalf("foreign installation target was accepted or wrapped: %v reason=%q", err, f.c.conflictReason)
	}
	if len(f.commands) != 0 || f.channels.Get("created").Int() != 0 {
		t.Fatal("foreign installation acquired message resources")
	}
	f.balanced(t)
}

func TestOfflineConflictDiagnosticResetsOnlyOnAdmissionAndDoesNotContaminateLaterResults(t *testing.T) {
	f := offlineBrowserFixture(t)
	f.registration.Set("scope", f.c.descriptor.Scope+"/child")
	if err := RefreshOfflineStatus(context.Background()); err != ErrOfflineConflict {
		t.Fatal("initial ownership conflict was not rejected", err)
	}
	firstDiagnostic := OfflineStatus().LastError
	f.c.busy = true
	if err := RefreshOfflineStatus(context.Background()); err != ErrOfflineBusy || f.c.conflictReason != "registration-scope" || OfflineStatus().LastError != firstDiagnostic {
		t.Fatal("an unadmitted operation erased the prior confirmed diagnostic", err)
	}
	f.c.busy = false
	f.registration.Set("scope", f.c.descriptor.Scope)
	originalReply := f.respond
	f.respond = func(request offlineRequest) offlineReply {
		reply := originalReply(request)
		reply.Version = 2
		return reply
	}
	if err := RefreshOfflineStatus(context.Background()); err != ErrOfflineProtocol || f.c.conflictReason != "" || OfflineStatus().LastError != ErrOfflineProtocol.Error() {
		t.Fatal("admitted protocol failure inherited an old ownership label", err)
	}
	f.respond = func(request offlineRequest) offlineReply {
		reply := originalReply(request)
		reply.State, reply.Enabled = "ready", true
		reply.ActiveGeneration = strings.Repeat("e", 64)
		return reply
	}
	f.sw.Set("controller", f.worker)
	if err := RefreshOfflineStatus(context.Background()); err != nil {
		t.Fatal("correct owned status stopped succeeding", err)
	}
	state := OfflineStatus()
	if f.c.conflictReason != "" || state.LastError != "" || state.Phase != "ready" || !state.Enabled || !state.Controlling || len(f.commands) != 2 || f.c.busy {
		t.Fatalf("successful status retained diagnostics or lost ownership: reason=%q state=%+v", f.c.conflictReason, state)
	}
	if f.registers != 0 || f.updates != 0 || f.unregisters != 0 {
		t.Fatal("diagnostic reporting changed registration state")
	}
	f.balanced(t)
}
