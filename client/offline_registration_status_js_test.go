//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"strings"
	"syscall/js"
	"testing"
)

func offlineUnprovenStatusFixture(t *testing.T) *offlineTestFixture {
	t.Helper()
	f := offlineBrowserFixture(t)
	f.registration.Set("active", js.Null())
	f.sw.Set("controller", f.worker)
	originalReply := f.respond
	f.respond = func(request offlineRequest) offlineReply {
		reply := originalReply(request)
		reply.State, reply.Enabled = "ready", true
		reply.ActiveGeneration = strings.Repeat("e", 64)
		return reply
	}
	return f
}

func TestOfflineStatusUsesOwnedControllerAfterInspectingExactScopeUnprovenRecord(t *testing.T) {
	f := offlineUnprovenStatusFixture(t)
	f.c.state.ActiveGeneration = strings.Repeat("f", 64)
	f.c.state.LastError = "an older unconfirmed operation"
	lists := 0
	f.sw.Set("getRegistrations", f.fn(func(js.Value, []js.Value) any {
		lists++
		return f.resolve(js.ValueOf([]any{f.registration}))
	}))
	if err := RefreshOfflineStatus(context.Background()); err != nil {
		t.Fatal("an exact owned controller could not report fresh status", err)
	}
	state := OfflineStatus()
	if lists != 1 || len(f.commands) != 1 || f.commands[0].Command != "status" || f.commands[0].OwnerPolicyID != f.c.descriptor.OwnerPolicyID {
		t.Fatalf("status did not make one ordinary validated observation: lists=%d commands=%+v", lists, f.commands)
	}
	if state.Phase != "ready" || !state.Enabled || !state.Controlling || state.ActiveGeneration != strings.Repeat("e", 64) || state.LastError != "" || f.c.conflictReason != "" || f.c.busy {
		t.Fatalf("status reused stale state or lost controller proof: %+v", state)
	}
	if f.registers != 0 || f.updates != 0 || f.unregisters != 0 {
		t.Fatal("status repaired or changed the unproven registration")
	}
	f.balanced(t)
}

func TestOfflineStatusUnprovenRecordNeedsOwnedController(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		name := "no-controller"
		if foreign {
			name = "foreign-controller"
		}
		t.Run(name, func(t *testing.T) {
			f := offlineUnprovenStatusFixture(t)
			f.sw.Set("controller", js.Null())
			want := ErrOfflineUnavailable
			if foreign {
				f.sw.Set("controller", js.ValueOf(map[string]any{"scriptURL": "https://reader.test/foreign-worker.js"}))
				want = ErrOfflineConflict
			}
			if err := RefreshOfflineStatus(context.Background()); err != want {
				t.Fatalf("unproved controller status changed error identity: got %v want %v", err, want)
			}
			if OfflineStatus().Phase != "error" || len(f.commands) != 0 || f.registers != 0 || f.updates != 0 || f.unregisters != 0 || f.c.busy {
				t.Fatal("unproved controller was queried or repaired")
			}
			if foreign && f.c.conflictReason != "controller-worker" {
				t.Fatalf("foreign controller lost ownership rejection: %q", f.c.conflictReason)
			}
			f.balanced(t)
		})
	}
}

func TestOfflineStatusUnprovenRecordDoesNotHideLaterOwnershipConflicts(t *testing.T) {
	cases := []struct {
		name, reason string
		configure    func(*offlineTestFixture)
	}{
		{"foreign-overlap-after", "registration-scope", func(f *offlineTestFixture) {
			foreign := js.ValueOf(map[string]any{"scope": "https://reader.test/", "active": f.worker})
			f.registrations = []js.Value{f.registration, foreign}
		}},
		{"foreign-overlap-without-controller", "registration-scope", func(f *offlineTestFixture) {
			f.sw.Set("controller", js.Null())
			foreign := js.ValueOf(map[string]any{"scope": "https://reader.test/", "active": f.worker})
			f.registrations = []js.Value{f.registration, foreign}
		}},
		{"foreign-worker-after", "registration-active-worker", func(f *offlineTestFixture) {
			foreign := js.ValueOf(map[string]any{"scope": f.c.descriptor.Scope, "active": map[string]any{"scriptURL": "https://reader.test/foreign-worker.js"}})
			f.registrations = []js.Value{f.registration, foreign}
		}},
		{"foreign-worker-before", "registration-active-worker", func(f *offlineTestFixture) {
			foreign := js.ValueOf(map[string]any{"scope": f.c.descriptor.Scope, "active": map[string]any{"scriptURL": "https://reader.test/foreign-worker.js"}})
			f.registrations = []js.Value{foreign, f.registration}
		}},
		{"duplicate-owned-after", "registration-duplicate", func(f *offlineTestFixture) {
			owned := js.ValueOf(map[string]any{"scope": f.c.descriptor.Scope, "active": f.worker, "waiting": nil, "installing": nil})
			f.registrations = []js.Value{f.registration, owned}
		}},
		{"duplicate-owned-before", "registration-duplicate", func(f *offlineTestFixture) {
			owned := js.ValueOf(map[string]any{"scope": f.c.descriptor.Scope, "active": f.worker, "waiting": nil, "installing": nil})
			f.registrations = []js.Value{owned, f.registration}
		}},
		{"duplicate-unproven", "registration-duplicate", func(f *offlineTestFixture) {
			f.registrations = []js.Value{f.registration, f.registration}
		}},
		{"malformed-later-scope", "registration-scope-type", func(f *offlineTestFixture) {
			f.registrations = []js.Value{f.registration, js.ValueOf(map[string]any{"scope": 42})}
		}},
		{"literal-prefix-overlap", "registration-scope", func(f *offlineTestFixture) {
			other := js.ValueOf(map[string]any{"scope": "https://reader.test/reader2", "active": f.worker})
			f.registrations = []js.Value{f.registration, other}
		}},
		{"unproven-scope-mismatch", "registration-scope", func(f *offlineTestFixture) {
			f.registration.Set("scope", f.c.descriptor.Scope+"/child")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := offlineUnprovenStatusFixture(t)
			tc.configure(f)
			if err := RefreshOfflineStatus(context.Background()); err != ErrOfflineConflict || f.c.conflictReason != tc.reason {
				t.Fatalf("unproven record hid an ownership conflict: err=%v reason=%q", err, f.c.conflictReason)
			}
			if len(f.commands) != 0 || f.registers != 0 || f.updates != 0 || f.unregisters != 0 || f.c.busy {
				t.Fatal("status acted before completing registration ownership checks")
			}
			f.balanced(t)
		})
	}
}

func TestOfflineStatusUnprovenRecordAllowsUnrelatedScopeWithoutReadingItsWorkers(t *testing.T) {
	f := offlineUnprovenStatusFixture(t)
	other := js.Global().Get("Function").New(`return {scope:"https://reader.test/unrelated/",get active(){throw Error("unrelated worker read");},get waiting(){throw Error("unrelated worker read");},get installing(){throw Error("unrelated worker read");}};`).Invoke()
	f.registrations = []js.Value{f.registration, other}
	if err := RefreshOfflineStatus(context.Background()); err != nil || len(f.commands) != 1 || f.commands[0].Command != "status" {
		t.Fatalf("unrelated scope changed controller status: err=%v commands=%+v", err, f.commands)
	}
	if f.registers != 0 || f.updates != 0 || f.unregisters != 0 {
		t.Fatal("unrelated scope caused a registration mutation")
	}
	f.balanced(t)
}

func TestOfflineStatusUnprovenRecordStillValidatesControllerReply(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*offlineReply)
	}{
		{"version", func(reply *offlineReply) { reply.Version = 2 }},
		{"owner", func(reply *offlineReply) { reply.OwnerPolicyID = "foreign-owner" }},
		{"request", func(reply *offlineReply) { reply.RequestID = "different-request" }},
		{"configuration", func(reply *offlineReply) { reply.ConfigurationID = "unproved" }},
		{"generation", func(reply *offlineReply) { reply.ActiveGeneration = "unproved" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := offlineUnprovenStatusFixture(t)
			originalReply := f.respond
			f.respond = func(request offlineRequest) offlineReply {
				reply := originalReply(request)
				tc.mutate(&reply)
				return reply
			}
			if err := RefreshOfflineStatus(context.Background()); err != ErrOfflineProtocol || OfflineStatus().Phase != "error" || f.c.conflictReason != "" {
				t.Fatalf("unproven registration bypassed reply validation: %v %+v", err, OfflineStatus())
			}
			if len(f.commands) != 1 || f.commands[0].Command != "status" || f.registers != 0 || f.updates != 0 || f.unregisters != 0 || f.c.busy {
				t.Fatal("invalid controller reply caused repair, mutation or held admission")
			}
			f.balanced(t)
		})
	}
}

func TestOfflineUnprovenRegistrationRemainsStrictForAllMutatingCommands(t *testing.T) {
	operations := []struct {
		name string
		call func(context.Context) error
	}{
		{"enable", EnableOffline},
		{"update", CheckOfflineUpdate},
		{"disable", DisableOffline},
	}
	for _, operation := range operations {
		for _, controller := range []string{"none", "owned", "foreign"} {
			t.Run(operation.name+"-"+controller, func(t *testing.T) {
				f := offlineUnprovenStatusFixture(t)
				if controller == "none" {
					f.sw.Set("controller", js.Null())
				} else if controller == "foreign" {
					f.sw.Set("controller", js.ValueOf(map[string]any{"scriptURL": "https://reader.test/foreign-worker.js"}))
				}
				wantReason := "registration-proof-unavailable"
				if controller == "foreign" {
					wantReason = "controller-worker"
				}
				if err := operation.call(context.Background()); err != ErrOfflineConflict || f.c.conflictReason != wantReason {
					t.Fatalf("mutation accepted an unproven registration: err=%v reason=%q", err, f.c.conflictReason)
				}
				if len(f.commands) != 0 || f.registers != 0 || f.updates != 0 || f.unregisters != 0 || f.c.busy {
					t.Fatal("rejected mutation changed registration or worker state")
				}
				f.balanced(t)
			})
		}
	}
}

func TestOfflineStatusUnprovenRecordPinsControllerUntilValidatedReply(t *testing.T) {
	for _, replacement := range []string{"none", "foreign"} {
		t.Run(replacement, func(t *testing.T) {
			f := offlineUnprovenStatusFixture(t)
			stats := js.ValueOf(map[string]any{"reads": 0})
			next := js.Null()
			if replacement == "foreign" {
				next = js.ValueOf(map[string]any{"scriptURL": "https://reader.test/foreign-worker.js"})
			}
			// Install the changing getter after the initial checking notification.
			// The scan sees the owned controller; later current-controller reads
			// see its replacement. The pinned worker still owes a valid reply.
			f.sw.Set("getRegistrations", f.fn(func(js.Value, []js.Value) any {
				js.Global().Get("Function").New("sw", "stats", "owned", "next", `
Object.defineProperty(sw, "controller", {configurable:true,get(){stats.reads++;return stats.reads===1?owned:next;}});
`).Invoke(f.sw, stats, f.worker, next)
				return f.resolve(js.ValueOf([]any{f.registration}))
			}))
			if err := RefreshOfflineStatus(context.Background()); err != nil {
				t.Fatal("the pinned owned worker could not report status", err)
			}
			state := OfflineStatus()
			if len(f.commands) != 1 || f.commands[0].Command != "status" || state.Phase != "ready" || !state.Enabled || state.Controlling || state.ActiveGeneration != strings.Repeat("e", 64) || state.LastError != "" {
				t.Fatalf("controller drift bypassed the reply or fabricated current control: commands=%+v state=%+v", f.commands, state)
			}
			if f.registers != 0 || f.updates != 0 || f.unregisters != 0 || f.c.busy {
				t.Fatal("controller drift caused registration repair or held admission")
			}
			f.balanced(t)
		})
	}
}

func TestOfflineStatusUnprovenRecordRejectsPinnedWorkerThatBecomesUnowned(t *testing.T) {
	f := offlineUnprovenStatusFixture(t)
	stats := js.ValueOf(map[string]any{"reads": 0})
	f.sw.Set("getRegistrations", f.fn(func(js.Value, []js.Value) any {
		js.Global().Get("Function").New("worker", "stats", "ownedURL", `
Object.defineProperty(worker, "scriptURL", {configurable:true,get(){stats.reads++;return stats.reads<=2?ownedURL:"https://reader.test/foreign-worker.js";}});
`).Invoke(f.worker, stats, f.c.descriptor.WorkerURL)
		return f.resolve(js.ValueOf([]any{f.registration}))
	}))
	if err := RefreshOfflineStatus(context.Background()); err != ErrOfflineConflict || f.c.conflictReason != "message-worker" {
		t.Fatalf("a changed pinned worker bypassed message ownership: err=%v reason=%q", err, f.c.conflictReason)
	}
	if len(f.commands) != 0 || OfflineStatus().Phase != "error" || f.registers != 0 || f.updates != 0 || f.unregisters != 0 || f.c.busy {
		t.Fatal("an unowned pinned worker was queried or caused registration changes")
	}
	f.balanced(t)
}
