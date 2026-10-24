//go:build js && wasm

package client

import (
	"syscall/js"
	"testing"
)

func TestHistoryTraversalUsesBrowserStackAndCancelsPendingWork(t *testing.T) {
	oldWin, oldDoc, oldURL, oldCancel, oldID := win, doc, currentURL, cancelNavigation, navigationID
	defer func() {
		win, doc, currentURL, cancelNavigation, navigationID = oldWin, oldDoc, oldURL, oldCancel, oldID
	}()
	remove := js.FuncOf(func(js.Value, []js.Value) any { return nil })
	defer remove.Release()
	node := js.ValueOf(map[string]any{"removeAttribute": remove})
	get := js.FuncOf(func(js.Value, []js.Value) any { return node })
	defer get.Release()
	doc = js.ValueOf(map[string]any{"getElementById": get})
	directions := []string{}
	canceled := 0
	back := js.FuncOf(func(js.Value, []js.Value) any { directions = append(directions, "back"); return nil })
	defer back.Release()
	forward := js.FuncOf(func(js.Value, []js.Value) any { directions = append(directions, "forward"); return nil })
	defer forward.Release()
	win = js.ValueOf(map[string]any{"history": map[string]any{"back": back, "forward": forward}})
	currentURL = ""
	if Back() == nil || Forward() == nil {
		t.Fatal("history accepted an unstarted runtime")
	}
	currentURL = "https://site.test/gallery"
	cancelNavigation = func() { canceled++ }
	if err := Back(); err != nil {
		t.Fatal(err)
	}
	cancelNavigation = func() { canceled++ }
	if err := Forward(); err != nil {
		t.Fatal(err)
	}
	if canceled != 2 || len(directions) != 2 || directions[0] != "back" || directions[1] != "forward" {
		t.Fatal(canceled, directions)
	}
}

func TestTrackedPushAndReplaceRecordTheActualPreviousDestination(t *testing.T) {
	oldWin, oldRegistry := win, ownedHistory
	defer func() { win, ownedHistory = oldWin, oldRegistry }()
	ownedHistory = historyRegistry{}
	history := js.ValueOf(map[string]any{"state": js.Null()})
	win = js.ValueOf(map[string]any{"Object": js.Global().Get("Object"), "history": history})
	initial := newHistoryState("https://site.test/gallery", "")
	history.Set("state", initial)
	push := navigationHistoryState("https://site.test/photos/one", "https://site.test/gallery", false)
	if record, ok := trackedHistory(push, "https://site.test/photos/one"); !ok || record.Previous != "https://site.test/gallery" {
		t.Fatal(record, ok)
	}
	history.Set("state", push)
	replace := navigationHistoryState("https://site.test/photos/two", "https://site.test/photos/one", true)
	if record, ok := trackedHistory(replace, "https://site.test/photos/two"); !ok || record.Previous != "https://site.test/gallery" {
		t.Fatal(record, ok)
	}
	history.Set("state", initial)
	replaceInitial := navigationHistoryState("https://site.test/photos/one", "https://site.test/gallery", true)
	if record, ok := trackedHistory(replaceInitial, "https://site.test/photos/one"); !ok || record.Previous != "" {
		t.Fatal("replace invented a previous entry", record, ok)
	}
	if _, ok := trackedHistory(js.ValueOf("invalid"), "https://site.test/gallery"); ok {
		t.Fatal("primitive history accepted")
	}
}
