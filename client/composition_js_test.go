//go:build js && wasm

package client

import (
	"encoding/json"
	"errors"
	"syscall/js"
	"testing"
)

func TestKeyedRowLifetimeInheritsProvidersAndDisposesEffect(t *testing.T) {
	parent := &Scope{}
	life, err := parent.Lifetime()
	if err != nil {
		t.Fatal(err)
	}
	key := NewContext[string]()
	Provide(life, key, "parent")
	setups, cleanups := 0, 0
	list := &KeyedList[int]{scope: parent, options: KeyedOptions[int]{Bind: func(row *Scope, n int) (func(int), error) {
		owner, err := row.Lifetime()
		if err != nil {
			return nil, err
		}
		if value, ok := UseContext(owner, key); !ok || value != "parent" {
			t.Fatal(value, ok)
		}
		state := NewState(n)
		_, err = owner.Effect(func() (func(), error) { setups++; return func() { cleanups++ }, nil }, state)
		return func(value int) { state.Set(value) }, err
	}}}
	// More than 128 rows exercise the separate lifetime and explicit-scope limits.
	rows := []*keyedRow[int]{}
	for i := 0; i < 200; i++ {
		row, err := list.bind(js.Undefined(), i)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	rows[0].update(1)
	rows[0].scope.dispose()
	rows[0].update(2)
	if setups != 201 || cleanups != 2 || len(life.children) != 199 {
		t.Fatal(setups, cleanups, len(life.children))
	}
	for _, row := range rows {
		row.scope.dispose()
	}
	if cleanups != 201 || len(life.children) != 0 {
		t.Fatal("row lifetimes retained", cleanups, len(life.children))
	}
	parent.dispose()
	if _, err := parent.Lifetime(); !errors.Is(err, ErrLifetimeClosed) {
		t.Fatal(err)
	}
}
func TestScopeLifetimeIsStableAndMountFailureDisposesIt(t *testing.T) {
	scope := &Scope{}
	life, err := scope.Lifetime()
	if err != nil {
		t.Fatal(err)
	}
	again, err := scope.Lifetime()
	if err != nil || again != life {
		t.Fatal("lifetime duplicated")
	}
	cleaned := 0
	life.Defer(func() { cleaned++ })
	err = mountScoped(scope, func(*Scope, json.RawMessage) error { return errors.New("mount failed") }, nil)
	if err == nil || !life.Closed() || cleaned != 1 {
		t.Fatal(err, cleaned)
	}
}

func TestSubscopeRejectsNonElementRootsBeforeDOMQueries(t *testing.T) {
	scope := &Scope{}
	for _, value := range []js.Value{js.Undefined(), js.Null(), js.ValueOf("text"), js.Global().Get("Object").New()} {
		if _, _, err := scope.Subscope(Element{value}); err == nil {
			t.Fatal("invalid root accepted")
		}
	}
}

func TestChildCallbacksStopBeforeParentCleanupDispatchesWork(t *testing.T) {
	locationEnvironment(t)
	selectionKey := selectionEnvironment(t)
	setLocation("https://site.test/initial")
	parent := &Scope{}
	child := &Scope{parent: parent}
	target := js.Global().Get("EventTarget").New()
	calls := 0
	child.On(Element{target}, "test", func(Event) { calls++ })
	observed := 0
	if _, err := child.ObserveLocation(func(Location) { observed++ }); err != nil {
		t.Fatal(err)
	}
	if _, err := child.ObserveSelection(selectionKey, func(Selection, bool) { observed++ }); err != nil {
		t.Fatal(err)
	}
	parent.Cleanup(func() {
		target.Call("dispatchEvent", js.Global().Get("Event").New("test"))
		setLocation("https://site.test/cleanup")
		notifyLocationObservers()
		installSelections(map[string]Selection{selectionKey: {Scope: selectionKey, Name: "children", Segments: []string{"cleanup"}}})
	})
	parent.dispose()
	if observed != 2 {
		t.Fatal("observer ran while ancestor was closing", observed)
	}
	if calls != 0 {
		t.Fatal("event ran while ancestor was closing", calls)
	}
	if _, err := child.Lifetime(); !errors.Is(err, ErrLifetimeClosed) {
		t.Fatal("created owner under closed ancestor", err)
	}
	cleaned := 0
	child.Cleanup(func() { cleaned++ })
	if cleaned != 1 {
		t.Fatal("late cleanup was retained")
	}
	child.dispose()
}
