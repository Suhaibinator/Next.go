//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"errors"
	"strings"
	"syscall/js"
	"testing"
	"time"
)

type reacquireTestFixture struct {
	f                        *offlineTestFixture
	candidate, probe         js.Value
	initial, final           []js.Value
	lists, lookups           int
	beforeProbe, beforeFinal func()
}

func reacquireFixture(t *testing.T) *reacquireTestFixture {
	t.Helper()
	f := offlineBrowserFixture(t)
	f.registration.Set("active", js.Null())
	f.sw.Set("controller", f.worker)
	r := &reacquireTestFixture{f: f}
	r.candidate = js.ValueOf(map[string]any{"scope": f.c.descriptor.Scope, "active": f.worker, "waiting": nil, "installing": nil})
	r.candidate.Set("update", f.fn(func(this js.Value, _ []js.Value) any {
		if !this.Equal(r.candidate) {
			t.Error("update receiver is not the proved registration")
		}
		f.updates++
		return f.resolve(r.candidate)
	}))
	r.candidate.Set("unregister", f.fn(func(this js.Value, _ []js.Value) any {
		if !this.Equal(r.candidate) {
			t.Error("unregister receiver is not the proved registration")
		}
		f.unregisters++
		return f.resolve(true)
	}))
	r.probe, r.initial, r.final = r.candidate, []js.Value{f.registration}, []js.Value{r.candidate}
	f.sw.Set("getRegistrations", f.fn(func(js.Value, []js.Value) any {
		r.lists++
		if r.lists == 1 {
			return f.resolve(reacquireArray(r.initial))
		}
		if r.beforeFinal != nil {
			r.beforeFinal()
		}
		return f.resolve(reacquireArray(r.final))
	}))
	f.sw.Set("getRegistration", f.fn(func(this js.Value, args []js.Value) any {
		r.lookups++
		if !this.Equal(f.sw) || len(args) != 1 || args[0].String() != f.c.descriptor.Scope {
			t.Error("targeted lookup lost the exact scope or native container")
		}
		if r.beforeProbe != nil {
			r.beforeProbe()
		}
		return f.resolve(r.probe)
	}))
	original := f.respond
	f.respond = func(request offlineRequest) offlineReply {
		reply := original(request)
		if request.Command == "status" {
			reply.State, reply.Enabled = "ready", true
			reply.ActiveGeneration = strings.Repeat("e", 64)
		}
		return reply
	}
	return r
}

func reacquireArray(items []js.Value) js.Value {
	array := js.Global().Get("Array").New(len(items))
	for i, item := range items {
		array.SetIndex(i, item)
	}
	return array
}

func (r *reacquireTestFixture) noEffects(t *testing.T) {
	t.Helper()
	f := r.f
	if len(f.commands) != 0 || f.registers != 0 || f.updates != 0 || f.unregisters != 0 || f.c.busy {
		t.Fatalf("rejected acquisition had effects or held admission: commands=%+v register=%d update=%d unregister=%d busy=%v", f.commands, f.registers, f.updates, f.unregisters, f.c.busy)
	}
	f.balanced(t)
}

func TestOfflineReacquisitionSupportsAllMutationsWithCurrentReplacementRecord(t *testing.T) {
	operations := []struct {
		name                           string
		call                           func(context.Context) error
		messages, updates, unregisters int
	}{
		{"enable", EnableOffline, 2, 0, 0},
		{"update", CheckOfflineUpdate, 1, 1, 0},
		{"disable", DisableOffline, 2, 0, 1},
	}
	for _, operation := range operations {
		for _, controlled := range []bool{false, true} {
			name := operation.name + map[bool]string{false: "-uncontrolled", true: "-controlled"}[controlled]
			t.Run(name, func(t *testing.T) {
				r := reacquireFixture(t)
				if !controlled {
					r.f.sw.Set("controller", js.Null())
				}
				if r.candidate.Equal(r.f.registration) {
					t.Fatal("test requires a current replacement record")
				}
				if err := operation.call(context.Background()); err != nil {
					t.Fatal(err)
				}
				f := r.f
				if r.lists != 2 || r.lookups != 1 || len(f.commands) != operation.messages || f.registers != 0 || f.updates != operation.updates || f.unregisters != operation.unregisters || f.c.busy {
					t.Fatalf("wrong bounded transaction or dispatch: lists=%d lookup=%d commands=%+v update=%d unregister=%d", r.lists, r.lookups, f.commands, f.updates, f.unregisters)
				}
				f.balanced(t)
			})
		}
	}
}

func TestOfflineReacquisitionDoesNotAssumeCachedNativeObjectIsRepaired(t *testing.T) {
	for _, populate := range []bool{false, true} {
		t.Run(map[bool]string{false: "still-empty", true: "same-object-now-owned"}[populate], func(t *testing.T) {
			r := reacquireFixture(t)
			r.candidate = r.f.registration
			r.probe, r.final = r.candidate, []js.Value{r.candidate}
			if populate {
				r.beforeProbe = func() { r.candidate.Set("active", r.f.worker) }
			}
			err := EnableOffline(context.Background())
			if !populate {
				if err != ErrOfflineConflict || r.lists != 1 || r.lookups != 1 || r.f.c.conflictReason != "registration-proof-empty-lookup" {
					t.Fatalf("cached empty object was trusted: %v", err)
				}
				r.noEffects(t)
			} else {
				if err != nil || r.lists != 2 || r.lookups != 1 || len(r.f.commands) != 2 {
					t.Fatalf("proved cached object did not work: %v", err)
				}
				r.f.balanced(t)
			}
		})
	}
}

func TestOfflineReacquisitionChecksEntireFirstInventoryBeforeTargetedRead(t *testing.T) {
	cases := []struct {
		name      string
		configure func(*reacquireTestFixture)
	}{
		{"later-foreign-scope", func(r *reacquireTestFixture) {
			r.initial = append(r.initial, js.ValueOf(map[string]any{"scope": "https://reader.test/", "active": r.f.worker}))
		}},
		{"literal-prefix-foreign", func(r *reacquireTestFixture) {
			r.initial = append(r.initial, js.ValueOf(map[string]any{"scope": "https://reader.test/reader2", "active": r.f.worker}))
		}},
		{"later-foreign-worker", func(r *reacquireTestFixture) {
			r.initial = append(r.initial, js.ValueOf(map[string]any{"scope": r.f.c.descriptor.Scope, "active": map[string]any{"scriptURL": "https://reader.test/foreign.js"}}))
		}},
		{"duplicate-empty", func(r *reacquireTestFixture) { r.initial = append(r.initial, r.f.registration) }},
		{"duplicate-owned", func(r *reacquireTestFixture) { r.initial = append(r.initial, r.candidate) }},
		{"later-malformed", func(r *reacquireTestFixture) { r.initial = append(r.initial, js.ValueOf(map[string]any{"scope": 42})) }},
		{"over-bound", func(r *reacquireTestFixture) { r.initial = make([]js.Value, 65) }},
		{"foreign-controller", func(r *reacquireTestFixture) {
			r.f.sw.Set("controller", js.ValueOf(map[string]any{"scriptURL": "https://reader.test/foreign.js"}))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := reacquireFixture(t)
			tc.configure(r)
			if err := CheckOfflineUpdate(context.Background()); err != ErrOfflineConflict || r.lists != 1 || r.lookups != 0 {
				t.Fatalf("invalid first inventory triggered recovery: %v lists=%d lookup=%d", err, r.lists, r.lookups)
			}
			r.noEffects(t)
		})
	}
}

func TestOfflineReacquisitionRejectsUnavailableOrForeignTargetedProof(t *testing.T) {
	cases := []struct {
		name      string
		configure func(*reacquireTestFixture)
		lookups   int
	}{
		{"unsupported", func(r *reacquireTestFixture) { r.f.sw.Set("getRegistration", js.Undefined()) }, 0},
		{"missing", func(r *reacquireTestFixture) { r.probe = js.Undefined() }, 1},
		{"null", func(r *reacquireTestFixture) { r.probe = js.Null() }, 1},
		{"malformed", func(r *reacquireTestFixture) { r.probe = js.ValueOf("not-a-registration") }, 1},
		{"empty", func(r *reacquireTestFixture) { r.candidate.Set("active", js.Null()) }, 1},
		{"broader-scope", func(r *reacquireTestFixture) { r.candidate.Set("scope", "https://reader.test/") }, 1},
		{"wrong-scope-type", func(r *reacquireTestFixture) { r.candidate.Set("scope", 42) }, 1},
		{"foreign-active", func(r *reacquireTestFixture) {
			r.candidate.Set("active", js.ValueOf(map[string]any{"scriptURL": "https://reader.test/foreign.js"}))
		}, 1},
		{"foreign-waiting", func(r *reacquireTestFixture) {
			r.candidate.Set("waiting", js.ValueOf(map[string]any{"scriptURL": "https://reader.test/foreign.js"}))
		}, 1},
		{"foreign-installing", func(r *reacquireTestFixture) {
			r.candidate.Set("installing", js.ValueOf(map[string]any{"scriptURL": "https://reader.test/foreign.js"}))
		}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := reacquireFixture(t)
			tc.configure(r)
			if err := EnableOffline(context.Background()); err != ErrOfflineConflict || r.lists != 1 || r.lookups != tc.lookups {
				t.Fatalf("unproved targeted result was accepted: %v lists=%d lookup=%d", err, r.lists, r.lookups)
			}
			r.noEffects(t)
		})
	}
}

func TestOfflineReacquisitionRejectsChangedSecondInventoryAndRevalidatesAfterIt(t *testing.T) {
	cases := []struct {
		name      string
		configure func(*reacquireTestFixture)
	}{
		{"missing", func(r *reacquireTestFixture) { r.final = nil }},
		{"different-native-identity", func(r *reacquireTestFixture) {
			r.final = []js.Value{js.ValueOf(map[string]any{"scope": r.f.c.descriptor.Scope, "active": r.f.worker})}
		}},
		{"duplicate", func(r *reacquireTestFixture) { r.final = append(r.final, r.candidate) }},
		{"foreign-overlap", func(r *reacquireTestFixture) {
			r.final = append(r.final, js.ValueOf(map[string]any{"scope": "https://reader.test/", "active": r.f.worker}))
		}},
		{"malformed-later", func(r *reacquireTestFixture) { r.final = append(r.final, js.ValueOf(map[string]any{"scope": 42})) }},
		{"over-bound", func(r *reacquireTestFixture) { r.final = make([]js.Value, 65) }},
		{"became-empty", func(r *reacquireTestFixture) { r.beforeFinal = func() { r.candidate.Set("active", js.Null()) } }},
		{"became-foreign", func(r *reacquireTestFixture) {
			r.beforeFinal = func() {
				r.candidate.Set("waiting", js.ValueOf(map[string]any{"scriptURL": "https://reader.test/foreign.js"}))
			}
		}},
		{"foreign-controller", func(r *reacquireTestFixture) {
			r.beforeFinal = func() {
				r.f.sw.Set("controller", js.ValueOf(map[string]any{"scriptURL": "https://reader.test/foreign.js"}))
			}
		}},
		{"lost-proof-during-final-controller-read", func(r *reacquireTestFixture) {
			r.beforeFinal = func() {
				js.Global().Get("Function").New("sw", "record", "worker", `Object.defineProperty(sw,"controller",{configurable:true,get(){record.active=null;return worker;}});`).Invoke(r.f.sw, r.candidate, r.f.worker)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := reacquireFixture(t)
			tc.configure(r)
			if err := DisableOffline(context.Background()); err != ErrOfflineConflict || r.lists != 2 || r.lookups != 1 {
				t.Fatalf("late proof change was accepted: %v lists=%d lookup=%d", err, r.lists, r.lookups)
			}
			if (tc.name == "became-empty" || tc.name == "lost-proof-during-final-controller-read") && r.f.c.conflictReason != "registration-proof-empty-final-inventory" {
				t.Fatalf("empty proof reported the wrong final-read stage: %q", r.f.c.conflictReason)
			}
			r.noEffects(t)
		})
	}
}

func TestOfflineReacquisitionPreservesNormalOwnedAbsentAndStatusReadCounts(t *testing.T) {
	cases := []struct {
		name                           string
		call                           func(context.Context) error
		owned, absent                  bool
		messages, updates, unregisters int
	}{
		{"owned-enable", EnableOffline, true, false, 2, 0, 0},
		{"owned-update", CheckOfflineUpdate, true, false, 1, 1, 0},
		{"owned-disable", DisableOffline, true, false, 2, 0, 1},
		{"owned-status", RefreshOfflineStatus, true, false, 1, 0, 0},
		{"unproven-status", RefreshOfflineStatus, false, false, 1, 0, 0},
		{"absent-controlled-status", RefreshOfflineStatus, false, true, 1, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := reacquireFixture(t)
			if tc.owned {
				r.initial = []js.Value{r.candidate}
			}
			if tc.absent {
				r.initial = nil
			}
			if err := tc.call(context.Background()); err != nil {
				t.Fatal(err)
			}
			f := r.f
			if r.lists != 1 || r.lookups != 0 || len(f.commands) != tc.messages || f.updates != tc.updates || f.unregisters != tc.unregisters || f.registers != 0 || f.c.busy {
				t.Fatalf("normal path acquired extra proof/calls: lists=%d lookup=%d commands=%+v", r.lists, r.lookups, f.commands)
			}
			f.balanced(t)
		})
	}
}

func TestOfflineReacquisitionCancellationKeepsPhysicalReadAdmissionUntilSettlement(t *testing.T) {
	for _, stage := range []string{"targeted", "final-inventory"} {
		t.Run(stage, func(t *testing.T) {
			r := reacquireFixture(t)
			started := make(chan struct{})
			resolve := js.Undefined()
			pending := js.Global().Get("Promise").New(r.f.fn(func(_ js.Value, args []js.Value) any { resolve = args[0]; return nil }))
			if stage == "targeted" {
				r.f.sw.Set("getRegistration", r.f.fn(func(js.Value, []js.Value) any { r.lookups++; close(started); return pending }))
			} else {
				r.f.sw.Set("getRegistrations", r.f.fn(func(js.Value, []js.Value) any {
					r.lists++
					if r.lists == 1 {
						return r.f.resolve(reacquireArray(r.initial))
					}
					close(started)
					return pending
				}))
			}
			ctx, cancel := context.WithCancelCause(context.Background())
			cause := errors.New("explicit acquisition caller cancellation")
			defer cancel(cause)
			done := make(chan error, 1)
			go func() { done <- CheckOfflineUpdate(ctx) }()
			joined := false
			settle := func() {
				value := r.candidate
				if stage == "final-inventory" {
					value = reacquireArray(r.final)
				}
				resolve.Invoke(value)
			}
			defer func() {
				settle()
				if !joined {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("physical read did not join before fixture cleanup")
					}
				}
			}()
			<-started
			cancel(cause)
			if !r.f.c.busy {
				t.Fatal("canceled native read released admission before settlement")
			}
			if err := RefreshOfflineStatus(context.Background()); err != ErrOfflineBusy {
				t.Fatal("reentrant status bypassed physical admission", err)
			}
			select {
			case err := <-done:
				joined = true
				t.Fatal("canceled read returned before physical settlement", err)
			default:
			}
			settle()
			err := <-done
			joined = true
			if err != ErrOfflineUnknown {
				t.Fatal("canceled native promise changed existing outcome semantics", err)
			}
			wantLists := 1
			if stage == "final-inventory" {
				wantLists = 2
			}
			if r.lists != wantLists || r.lookups != 1 {
				t.Fatalf("cancellation started later proof stages: lists=%d lookup=%d", r.lists, r.lookups)
			}
			r.noEffects(t)
		})
	}
}

func TestOfflineRecoveredMutationChecksOwnershipAfterAwaitedMessages(t *testing.T) {
	cases := []struct {
		name, command string
		mutate        func(*reacquireTestFixture)
		messages      int
	}{
		{"enable-status-lost-active", "enable", func(r *reacquireTestFixture) { r.candidate.Set("active", js.Null()) }, 1},
		{"disable-status-lost-active", "disable", func(r *reacquireTestFixture) { r.candidate.Set("active", js.Null()) }, 1},
		{"enable-status-scope-change", "enable", func(r *reacquireTestFixture) { r.candidate.Set("scope", "https://reader.test/") }, 1},
		{"disable-status-foreign-controller", "disable", func(r *reacquireTestFixture) {
			r.f.sw.Set("controller", js.ValueOf(map[string]any{"scriptURL": "https://reader.test/foreign.js"}))
		}, 1},
		{"disable-completed-blocks-unregister", "disable-completed", func(r *reacquireTestFixture) { r.candidate.Set("active", js.Null()) }, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := reacquireFixture(t)
			original := r.f.respond
			r.f.respond = func(request offlineRequest) offlineReply {
				reply := original(request)
				if tc.command == "disable-completed" && request.Command == "disable" || tc.command != "disable-completed" && request.Command == "status" {
					tc.mutate(r)
				}
				return reply
			}
			call := DisableOffline
			if tc.command == "enable" {
				call = EnableOffline
			}
			if err := call(context.Background()); err != ErrOfflineConflict {
				t.Fatal("post-message ownership loss allowed further mutation", err)
			}
			wantEmptyReason := ""
			switch tc.name {
			case "enable-status-lost-active", "disable-status-lost-active":
				wantEmptyReason = "registration-proof-empty-message"
			case "disable-completed-blocks-unregister":
				wantEmptyReason = "registration-proof-empty-unregister"
			}
			if wantEmptyReason != "" && r.f.c.conflictReason != wantEmptyReason {
				t.Fatalf("empty proof reported the wrong dispatch stage: %q", r.f.c.conflictReason)
			}
			f := r.f
			if r.lists != 2 || r.lookups != 1 || len(f.commands) != tc.messages || f.registers != 0 || f.updates != 0 || f.unregisters != 0 || f.c.busy {
				t.Fatalf("post-message rejection had extra effects: %+v", f.commands)
			}
			// A completed disable message is not rolled back. Only its later
			// unregister is forbidden once fresh ownership proof is lost.
			f.balanced(t)
		})
	}
}

func TestOfflineRecoveredUpdateRechecksProofImmediatelyBeforeDispatch(t *testing.T) {
	r := reacquireFixture(t)
	r.beforeFinal = func() {
		js.Global().Get("Function").New("sw", "record", "worker", `let reads=0;Object.defineProperty(sw,"controller",{configurable:true,get(){if(++reads>=2)record.active=null;return worker;}});`).Invoke(r.f.sw, r.candidate, r.f.worker)
	}
	if err := CheckOfflineUpdate(context.Background()); err != ErrOfflineConflict || r.lists != 2 || r.lookups != 1 || r.f.c.conflictReason != "registration-proof-empty-update" {
		t.Fatal("pre-dispatch ownership loss was accepted", err)
	}
	r.noEffects(t)
}

func TestOfflineReacquisitionLeavesAbsentRegistrationBehaviorUnchanged(t *testing.T) {
	cases := []struct {
		name                             string
		call                             func(context.Context) error
		controlled                       bool
		messages, registers, unregisters int
		want                             error
	}{
		{"enable", EnableOffline, true, 2, 1, 0, nil},
		{"disable-controlled", DisableOffline, true, 2, 0, 0, nil},
		{"disable-uncontrolled", DisableOffline, false, 2, 1, 1, nil},
		{"update", CheckOfflineUpdate, true, 0, 0, 0, ErrOfflineUnavailable},
		{"status-uncontrolled", RefreshOfflineStatus, false, 0, 0, 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := reacquireFixture(t)
			r.initial = nil
			r.f.registration.Set("active", r.f.worker)
			if !tc.controlled {
				r.f.sw.Set("controller", js.Null())
			}
			if err := tc.call(context.Background()); err != tc.want {
				t.Fatalf("absent registration changed behavior: got %v want %v", err, tc.want)
			}
			f := r.f
			if r.lists != 1 || r.lookups != 0 || len(f.commands) != tc.messages || f.registers != tc.registers || f.unregisters != tc.unregisters || f.updates != 0 || f.c.busy {
				t.Fatalf("absent path started extra acquisition/effects: lists=%d lookup=%d commands=%+v register=%d unregister=%d", r.lists, r.lookups, f.commands, f.registers, f.unregisters)
			}
			f.balanced(t)
		})
	}
}

func TestOfflineReacquisitionCancellationFencesBeforeAdmissionAndAfterLastRead(t *testing.T) {
	for _, afterRead := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-admission", true: "after-final-read"}[afterRead], func(t *testing.T) {
			r := reacquireFixture(t)
			ctx, cancel := context.WithCancelCause(context.Background())
			cause := errors.New("known cancellation before mutation")
			defer cancel(cause)
			if !afterRead {
				cancel(cause)
			} else {
				cancelFunc := r.f.fn(func(js.Value, []js.Value) any { cancel(cause); return nil })
				r.beforeFinal = func() {
					js.Global().Get("Function").New("sw", "cancel", "worker", `Object.defineProperty(sw,"controller",{configurable:true,get(){cancel();return worker;}});`).Invoke(r.f.sw, cancelFunc, r.f.worker)
				}
			}
			if err := CheckOfflineUpdate(ctx); err != cause {
				t.Fatal("known cancellation lost its cause or dispatched", err)
			}
			wantLists, wantLookup := 0, 0
			if afterRead {
				wantLists, wantLookup = 2, 1
			}
			if r.lists != wantLists || r.lookups != wantLookup {
				t.Fatalf("cancellation crossed acquisition stages: lists=%d lookup=%d", r.lists, r.lookups)
			}
			r.noEffects(t)
		})
	}
}

func TestOfflineReacquisitionKeepsNativeReadRejectionsAndMalformedListFailClosed(t *testing.T) {
	for _, stage := range []string{"targeted-reject", "final-reject", "final-malformed"} {
		t.Run(stage, func(t *testing.T) {
			r := reacquireFixture(t)
			rejected := func() js.Value {
				return js.Global().Get("Promise").Call("reject", js.Global().Get("Error").New("fixed test read rejection"))
			}
			if stage == "targeted-reject" {
				r.f.sw.Set("getRegistration", r.f.fn(func(js.Value, []js.Value) any { r.lookups++; return rejected() }))
			} else {
				r.f.sw.Set("getRegistrations", r.f.fn(func(js.Value, []js.Value) any {
					r.lists++
					if r.lists == 1 {
						return r.f.resolve(reacquireArray(r.initial))
					}
					if stage == "final-malformed" {
						return r.f.resolve("not-an-inventory")
					}
					return rejected()
				}))
			}
			err := CheckOfflineUpdate(context.Background())
			if err == nil || stage == "final-malformed" && err != ErrOfflineConflict || stage != "final-malformed" && err == ErrOfflineConflict {
				t.Fatal("native rejection was fabricated into ownership proof or changed to a conflict", err)
			}
			wantLists := 2
			if stage == "targeted-reject" {
				wantLists = 1
			}
			if r.lists != wantLists || r.lookups != 1 {
				t.Fatalf("read rejection crossed bounded stages: lists=%d lookup=%d", r.lists, r.lookups)
			}
			r.noEffects(t)
		})
	}
}

func TestOfflineReacquisitionRejectsUnprovedAuthorityForEveryMutation(t *testing.T) {
	operations := []struct {
		name string
		call func(context.Context) error
	}{
		{"enable", EnableOffline}, {"update", CheckOfflineUpdate}, {"disable", DisableOffline},
	}
	for _, operation := range operations {
		for _, failure := range []string{"empty-proof", "unmatched-latest"} {
			t.Run(operation.name+"-"+failure, func(t *testing.T) {
				r := reacquireFixture(t)
				if failure == "empty-proof" {
					r.candidate.Set("active", js.Null())
				} else {
					r.final = []js.Value{js.ValueOf(map[string]any{"scope": r.f.c.descriptor.Scope, "active": r.f.worker})}
				}
				if err := operation.call(context.Background()); err != ErrOfflineConflict {
					t.Fatal("unproved authority changed the exact rejection identity", err)
				}
				wantLists := 1
				if failure == "unmatched-latest" {
					wantLists = 2
				}
				if r.lists != wantLists || r.lookups != 1 {
					t.Fatalf("failed acquisition exceeded fixed read stages: lists=%d lookup=%d", r.lists, r.lookups)
				}
				r.noEffects(t)
			})
		}
	}
}
