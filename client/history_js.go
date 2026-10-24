//go:build js && wasm

package client

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"syscall/js"
)

var ownedHistory historyRegistry

func trackedHistory(state js.Value, current string) (historyRecord, bool) {
	if state.IsNull() || state.IsUndefined() || state.Type() != js.TypeObject {
		return historyRecord{}, false
	}
	key := state.Get("nextgoEntry")
	if key.Type() != js.TypeString || len(key.String()) != 32 {
		return historyRecord{}, false
	}
	return ownedHistory.get(key.String(), current)
}
func newHistoryState(target, previous string) js.Value {
	state := win.Get("Object").New()
	var random [16]byte
	if _, err := rand.Read(random[:]); err == nil {
		key := hex.EncodeToString(random[:])
		if ownedHistory.put(key, historyRecord{target, previous}) {
			state.Set("nextgoEntry", key)
		}
	}
	return state
}
func navigationHistoryState(target, previous string, replace bool) js.Value {
	if replace {
		record, ok := trackedHistory(win.Get("history").Get("state"), previous)
		previous = ""
		if ok {
			previous = record.Previous
		}
	}
	return newHistoryState(target, previous)
}

// BackOrReplace returns to a same-application page. It goes back only when this
// runtime tracked that exact previous destination; otherwise it replaces the
// current entry. An omitted target fragment accepts the previous fragment.
// Unknown, evicted or other-document entries use replacement, not guesswork.
func BackOrReplace(target string) (err error) {
	if moduleMode {
		return moduleCall("return", target)
	}
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("return navigation: %v", value)
		}
	}()
	u, err := pageURL(target)
	if err != nil {
		return err
	}
	record, ok := trackedHistory(win.Get("history").Get("state"), currentURL)
	if ok && returnTargetMatches(record.Previous, u.Get("href").String()) {
		return Back()
	}
	return Replace(u.Get("href").String())
}

// Back traverses browser history, including entries outside this application.
// Like a browser back button it has no effect when there is no previous entry.
// Go navigation handles same-application popstate work asynchronously.
func Back() error {
	if moduleMode {
		return moduleCall("back")
	}
	return traverseHistory("back")
}
func Forward() error {
	if moduleMode {
		return moduleCall("forward")
	}
	return traverseHistory("forward")
}
func traverseHistory(direction string) (err error) {
	if currentURL == "" {
		return fmt.Errorf("navigation runtime has not started")
	}
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("history traversal: %v", v)
		}
	}()
	cancelPendingNavigation()
	win.Get("history").Call(direction)
	return nil
}
