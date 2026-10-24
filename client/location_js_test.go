//go:build js && wasm

package client

import (
	"syscall/js"
	"testing"
)

func locationEnvironment(t *testing.T) {
	t.Helper()
	oldWin, oldLocation, oldObservers := win, committedLocation, locationObservers
	win = js.ValueOf(map[string]any{"URL": js.Global().Get("URL"), "location": map[string]any{"href": "https://site.test/pending"}})
	committedLocation = Location{}
	locationObservers = map[uint64]*locationObserver{}
	t.Cleanup(func() { win, committedLocation, locationObservers = oldWin, oldLocation, oldObservers })
}

func TestLocationSubscriptionsFollowOnlyCommittedChanges(t *testing.T) {
	locationEnvironment(t)
	s := &Scope{}
	defer s.dispose()
	if _, ready := CurrentLocation(); ready {
		t.Fatal("location available before Run")
	}
	if _, err := s.ObserveLocation(func(Location) {}); err == nil {
		t.Fatal("observed before Run")
	}
	setLocation("https://site.test/shop/fr/a%20b?tag=one&tag=two#part")
	var seen []Location
	stop, err := s.ObserveLocation(func(value Location) { seen = append(seen, value) })
	if err != nil || len(seen) != 1 || seen[0].Pathname != "/shop/fr/a%20b" || seen[0].Search != "?tag=one&tag=two" || seen[0].Hash != "#part" {
		t.Fatal(err, seen)
	}
	notifyLocationObservers()
	if len(seen) != 1 {
		t.Fatal("unchanged refresh notified")
	}
	// The live address can differ while a history request is pending or failed.
	if value, _ := CurrentLocation(); value.URL != seen[0].URL {
		t.Fatal("pending URL leaked", value)
	}
	setLocation("https://site.test/shop/fr/next?q=new")
	if len(seen) != 1 {
		t.Fatal("notified before commit finished")
	}
	// An island mounted during commit receives the new value immediately and
	// must not receive a second notification at the end of that same commit.
	newCalls := 0
	_, err = s.ObserveLocation(func(Location) { newCalls++ })
	if err != nil {
		t.Fatal(err)
	}
	notifyLocationObservers()
	if len(seen) != 2 || newCalls != 1 {
		t.Fatal(seen, newCalls)
	}
	stop()
	stop()
	setLocation("https://site.test/shop/fr/next?q=new#hash")
	notifyLocationObservers()
	if len(seen) != 2 || newCalls != 2 {
		t.Fatal("unsubscribe failed", seen, newCalls)
	}
	s.dispose()
	if len(locationObservers) != 0 {
		t.Fatal("observers survived disposal")
	}
}

func TestLocationObserverBoundsAndReentrantCleanup(t *testing.T) {
	locationEnvironment(t)
	setLocation("https://site.test/")
	s := &Scope{}
	if _, err := s.ObserveLocation(nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	for i := 0; i < 1000; i++ {
		stop, err := s.ObserveLocation(func(Location) {})
		if err != nil {
			t.Fatal(err)
		}
		stop()
	}
	if len(s.cleanup) != 1 {
		t.Fatal("unsubscribe cycles accumulated cleanup closures", len(s.cleanup))
	}
	calls := 0
	_, err := s.ObserveLocation(func(Location) {
		calls++
		if calls == 2 {
			s.dispose()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < maxLocationObservers; i++ {
		if _, err := s.ObserveLocation(func(Location) {}); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := s.ObserveLocation(func(Location) {}); err == nil {
		t.Fatal("unbounded observers")
	}
	setLocation("https://site.test/next")
	notifyLocationObservers()
	setLocation("https://site.test/last")
	notifyLocationObservers()
	if calls != 2 || len(locationObservers) != 0 {
		t.Fatal("reentrant disposal failed", calls, len(locationObservers))
	}
	if _, err := s.ObserveLocation(func(Location) {}); err == nil {
		t.Fatal("closed scope accepted subscription")
	}
}
