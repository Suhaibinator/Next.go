//go:build js && wasm

package client

import "fmt"

var committedLocation Location

type locationObserver struct {
	owner *Scope
	fn    func(Location)
	last  Location
}

var locationObservers = map[uint64]*locationObserver{}
var nextLocationObserver uint64

const maxLocationObservers = 512

// CurrentLocation returns the last committed URL, not a pending history target.
// It is unavailable until Run initializes the document location, even if
// subsequent validation disables SPA routing and keeps ordinary navigation.
func CurrentLocation() (Location, bool) {
	return committedLocation, committedLocation.URL != ""
}

// CurrentRouteParams matches pattern against the committed visible pathname
// using this document's application base. It is unavailable before Run. Lazy
// components use the same committed location and paths supplied by the core;
// matching does not establish which route is authorized or selected.
func CurrentRouteParams(pattern string) (map[string]string, bool) {
	location, ready := CurrentLocation()
	if !ready {
		return nil, false
	}
	return location.RouteParams(pattern, deployment.BasePath)
}

// ObserveLocation synchronizes immediately and then observes URL changes after
// successful commits, including same-page fragment changes and back/forward.
// Refreshing the same URL does not notify. Cleanup automatically unsubscribes;
// the returned function permits earlier, idempotent unsubscription. Callbacks
// run on the Go/WASM event loop and must return promptly.
func (s *Scope) ObserveLocation(fn func(Location)) (func(), error) {
	if !s.active() || fn == nil {
		return nil, fmt.Errorf("location observer requires an open scope and callback")
	}
	if _, ready := CurrentLocation(); !ready {
		return nil, fmt.Errorf("location observer requires Run")
	}
	if len(locationObservers) >= maxLocationObservers {
		return nil, fmt.Errorf("location observer bound exceeded")
	}
	nextLocationObserver++
	id := nextLocationObserver
	observer := &locationObserver{owner: s, fn: fn}
	locationObservers[id] = observer
	stop := func() { delete(locationObservers, id) }
	if !s.locationCleanup {
		s.locationCleanup = true
		s.Cleanup(func() {
			for id, observer := range locationObservers {
				if observer.owner == s {
					delete(locationObservers, id)
				}
			}
		})
	}
	dispatchLocation(observer)
	return stop, nil
}

func dispatchLocation(observer *locationObserver) {
	if !observer.owner.active() || observer.last == committedLocation {
		return
	}
	if observer.owner.mergeCallbackGuard() != nil && mergeQueueNotification(observer.owner, fmt.Sprintf("location:%p", observer), func() { dispatchLocation(observer) }) {
		return
	}
	observer.last = committedLocation
	defer func() {
		if recovered := recover(); recovered != nil {
			report(fmt.Sprintf("location observer: %v", recovered))
		}
	}()
	if guard := observer.owner.mergeCallbackGuard(); guard != nil {
		reportOwner := mergeRootScope(observer.owner)
		if err := guard(func() {
			if observer.owner.active() {
				observer.fn(committedLocation)
			}
		}); err != nil {
			reportScopeError(reportOwner, err.Error())
		}
	} else {
		observer.fn(committedLocation)
	}
}

// Set before mounting new islands or notifying selection observers so every
// callback reads the same committed URL. Notify after the DOM/head/history work.
func setLocation(target string) {
	u := win.Get("URL").New(target)
	committedLocation = Location{URL: u.Get("href").String(), Pathname: u.Get("pathname").String(), Search: u.Get("search").String(), Hash: u.Get("hash").String()}
	if !mergeInstallActive {
		propsLocationChanged(committedLocation)
	}
}

func notifyLocationObservers() {
	if !moduleMode {
		syncLazyModules()
	}
	ids := make([]uint64, 0, len(locationObservers))
	for id := range locationObservers {
		ids = append(ids, id)
	}
	for _, id := range ids {
		if observer, ok := locationObservers[id]; ok {
			dispatchLocation(observer)
		}
	}
}
