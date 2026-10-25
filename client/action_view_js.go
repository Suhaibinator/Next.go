//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"github.com/Suhaibinator/Next.go/internal/actionview"
	"github.com/Suhaibinator/Next.go/internal/navigation"
)

// ActionViewOutcome describes the refreshed view, independently of mutation
// confirmation. Applied can be true with Err when deferred content failed.
type ActionViewOutcome struct {
	Applied bool
	Status  int
	Err     error
}

var frameworkActionSequence uint64
var activeActionView *actionViewOwner
var actionViewFrozen bool

type actionViewCommitKey struct{}
type actionViewOwner struct {
	ctx                                                 context.Context
	cancel                                              context.CancelFunc
	caller                                              context.Context
	mu                                                  sync.Mutex
	detached                                            bool
	refresh                                             bool
	commitState                                         js.Value
	sequence, navigation                                uint64
	source, build, configuration, draft, history, slots string
}

func cancelActionView() {
	if activeActionView != nil {
		activeActionView.cancel()
	}
}
func beginFrameworkAction() {
	if moduleMode {
		_ = moduleCall("actionFence")
		return
	}
	frameworkActionSequence++
	cancelActionView()
	invalidateCache()
}
func installActionViewLifecycle() {
	listen(win, "pagehide", func(js.Value) { actionViewFrozen = true; cancelActionView() })
	listen(win, "pageshow", func(js.Value) { actionViewFrozen = false })
}
func actionHistoryEntry() string {
	state := win.Get("history").Get("state")
	if state.Type() == js.TypeObject && !state.IsNull() {
		if id := state.Get("nextgoEntry"); id.Type() == js.TypeString {
			return id.String()
		}
	}
	return win.Get("JSON").Call("stringify", state).String()
}
func (o *actionViewOwner) check() error {
	o.mu.Lock()
	detached := o.detached
	o.mu.Unlock()
	if err := o.ctx.Err(); err != nil {
		return err
	}
	if !detached && o.caller.Err() != nil {
		return o.caller.Err()
	}
	if activeActionView != o || frameworkActionSequence != o.sequence || navigationID != o.navigation || currentURL != o.source || win.Get("location").Get("href").String() != o.source || draftFrozen || actionViewFrozen || documentDraft() != o.draft || documentConfiguration() != o.configuration || doc.Get("body").Call("getAttribute", "data-nextgo-build").String() != o.build || actionHistoryEntry() != o.history {
		return fmt.Errorf("action view was superseded")
	}
	if routeManifest.Parallel != "" {
		slots, err := currentSlotState()
		if err != nil || slots != o.slots {
			return fmt.Errorf("action view slot ownership changed")
		}
	}
	return nil
}
func (o *actionViewOwner) commitGuard() error {
	if err := o.check(); err != nil {
		return err
	}
	if o.commitState.Truthy() {
		if o.refresh {
			// The source can scroll while transport or a transition is pending.
			// Stamp only the detached carrier at the actual guarded update; the
			// old history entry and unrelated user state remain untouched here.
			o.commitState.Set("nextgoX", win.Get("scrollX"))
			o.commitState.Set("nextgoY", win.Get("scrollY"))
		} else {
			// A redirect creates a new entry. Preserve the source's live scroll
			// for back navigation immediately before that push, not at dispatch.
			saveScroll()
		}
	}
	// No await occurs between detaching and the synchronous commit. Its own
	// island cleanup may cancel the source context; the remaining view transport
	// belongs to the document and remains bounded by o.ctx's timeout/fences.
	o.mu.Lock()
	o.detached = true
	o.mu.Unlock()
	return nil
}
func (o *actionViewOwner) prepareCommitState(refresh bool) js.Value {
	o.refresh = refresh
	object := js.Global().Get("Object")
	// A null prototype also preserves an unrelated own __proto__ user key
	// without invoking Object.prototype's legacy setter during the copy.
	o.commitState = object.Call("assign", object.Call("create", js.Null()), win.Get("history").Get("state"))
	return o.commitState
}
func (o *actionViewOwner) afterCommit() error {
	o.source = currentURL
	o.history = actionHistoryEntry()
	if routeManifest.Parallel != "" {
		var err error
		o.slots, err = currentSlotState()
		return err
	}
	return nil
}

// ActionView performs one action POST and consumes its negotiated fresh view.
// validate runs synchronously on the ordinary typed result envelope before any
// DOM commit. An error returned before that validation succeeds is a mutation
// error/unknown outcome; after confirmation only Outcome.Err reports failures.
// Network work must run in a goroutine outside a synchronous browser callback.
func ActionView(ctx context.Context, target string, request FetchOptions, validate func(Response) error) (outcome ActionViewOutcome, err error) {
	if moduleMode {
		return actionViewFromHost(ctx, target, request, validate)
	}
	confirmed := false
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("action view: %v", failure)
		}
		if confirmed && err != nil {
			outcome.Err = err
			err = nil
		}
	}()
	if ctx == nil || validate == nil {
		return outcome, fmt.Errorf("action view requires context and result validator")
	}
	if err = ctx.Err(); err != nil {
		return outcome, err
	}
	if currentURL == "" || staticExport() || draftFrozen || actionViewFrozen || !frameworkActionRequest(target, "POST") {
		return outcome, fmt.Errorf("action view is unavailable for this document or endpoint")
	}
	endpointURL := win.Get("URL").New(target, doc.Get("baseURI"))
	if endpointURL.Get("hash").String() != "" {
		return outcome, fmt.Errorf("action endpoint cannot contain a fragment")
	}
	endpoint := endpointURL.Get("href").String()
	cancelPendingNavigation()
	beginFrameworkAction()
	viewCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	owner := &actionViewOwner{ctx: viewCtx, cancel: cancel, caller: ctx, sequence: frameworkActionSequence, navigation: navigationID, source: currentURL, build: doc.Get("body").Call("getAttribute", "data-nextgo-build").String(), configuration: documentConfiguration(), draft: documentDraft(), history: actionHistoryEntry()}
	if routeManifest.Parallel != "" {
		owner.slots, err = currentSlotState()
		if err != nil {
			cancel()
			return outcome, err
		}
	}
	activeActionView = owner
	stopCaller := context.AfterFunc(ctx, func() {
		owner.mu.Lock()
		defer owner.mu.Unlock()
		if !owner.detached {
			cancel()
		}
	})
	defer func() {
		stopCaller()
		cancel()
		if activeActionView == owner {
			activeActionView = nil
		}
		invalidateCache()
	}()
	viewCtx = context.WithValue(viewCtx, actionViewCommitKey{}, owner.commitGuard)
	sourceURL := win.Get("URL").New(owner.source)
	sourceURL.Set("hash", "")
	headers := map[string]string{}
	for key, value := range request.Headers {
		headers[key] = value
	}
	for _, key := range []string{navigation.ShellHeader, navigation.ShellProtocolHeader, navigation.PurposeHeader, actionview.Header, actionview.URLHeader, actionview.BuildHeader, navigation.ProtocolHeader, navigation.ConfigurationHeader, navigation.DraftHeader, navigation.SlotsHeader, "Accept"} {
		for existing := range headers {
			if strings.EqualFold(existing, key) {
				delete(headers, existing)
			}
		}
	}
	headers[actionview.Header] = fmt.Sprint(actionview.Version)
	headers[actionview.URLHeader] = sourceURL.Get("pathname").String() + sourceURL.Get("search").String()
	if len(headers[actionview.URLHeader]) > actionview.MaxURL {
		return outcome, fmt.Errorf("current action view URL exceeds byte limit")
	}
	headers[actionview.BuildHeader] = owner.build
	headers[navigation.ProtocolHeader] = fmt.Sprint(navigation.ProtocolVersion)
	headers[navigation.ConfigurationHeader] = owner.configuration
	headers[navigation.DraftHeader] = owner.draft
	if owner.slots != "" {
		headers[navigation.SlotsHeader] = owner.slots
	}
	headers["Accept"] = actionview.MediaType
	request.Headers = headers
	request.Method = "POST"
	request.Cache = "no-store"
	request.Redirect = "error"
	request.MaxBytes = actionview.MaxBytes
	request.skipActionFence = true
	var response Response
	var buffered []byte
	var decoder actionview.Decoder
	var stream *navigationStream
	streaming := false
	viewTarget := owner.source
	defer func() {
		if stream != nil && activeNavigationStream == stream {
			activeNavigationStream = nil
		}
		if err != nil && stream != nil {
			stream.failPending()
		}
	}()
	_, err = FetchStream(viewCtx, endpoint, request, func(value Response) error {
		response = value
		if value.URL != endpoint {
			return fmt.Errorf("action endpoint changed")
		}
		media := strings.ToLower(strings.TrimSpace(strings.Split(value.Header.Get("Content-Type"), ";")[0]))
		streaming = media == actionview.MediaType
		if !streaming {
			if media != "application/json" {
				return fmt.Errorf("action returned an unsupported representation")
			}
			return nil
		}
		if value.StatusCode != 200 || value.Header.Get(actionview.Header) != fmt.Sprint(actionview.Version) || value.Header.Get(navigation.ProtocolHeader) != fmt.Sprint(navigation.ProtocolVersion) || value.Header.Get(navigation.ConfigurationHeader) != owner.configuration || value.Header.Get(navigation.DraftHeader) != owner.draft {
			return fmt.Errorf("action view response identity changed")
		}
		return owner.check()
	}, func(chunk []byte) error {
		if !streaming {
			if len(buffered)+len(chunk) > actionview.MaxResult {
				return ErrResponseTooLarge
			}
			buffered = append(buffered, chunk...)
			return nil
		}
		return decoder.Feed(chunk, func(frame actionview.Frame) error {
			if frame.Type == "result" {
				value := response
				value.Body = frame.Result
				if err := validate(value); err != nil {
					return err
				}
				confirmed = true
				var result struct {
					Redirect string `json:"redirect"`
				}
				if json.Unmarshal(frame.Result, &result) != nil {
					return fmt.Errorf("invalid action redirect metadata")
				}
				if result.Redirect != "" {
					destination := win.Get("URL").New(result.Redirect, owner.source)
					if !eligibleURL(destination) {
						return fmt.Errorf("action redirect leaves application")
					}
					viewTarget = destination.Get("href").String()
				}
				return owner.check()
			}
			if err := owner.check(); err != nil {
				return err
			}
			switch frame.Type {
			case "view-error":
				outcome.Status = frame.Error.Status
				return fmt.Errorf("action confirmed; view failed (%s): %s", frame.Error.Code, frame.Error.Message)
			case "shell":
				var fields map[string]json.RawMessage
				if json.Unmarshal(frame.Page, &fields) != nil {
					return fmt.Errorf("invalid action page")
				}
				for _, name := range []string{"shell", "shellRef", "ttl"} {
					if _, present := fields[name]; present {
						return fmt.Errorf("action view contains public cache fields")
					}
				}
				var status struct {
					Status int `json:"status"`
				}
				if json.Unmarshal(frame.Page, &status) != nil || status.Status < 200 || status.Status >= 600 {
					return fmt.Errorf("invalid action view status")
				}
				synthetic := response
				synthetic.URL = sourceURL.Get("href").String()
				synthetic.StatusCode = status.Status
				data, _, fallback, e := decodeEnvelopeResponse(synthetic, frame.Page, viewTarget, time.Now(), true)
				if e != nil {
					return e
				}
				if fallback || data.Shell != "" || data.ShellRef {
					return fmt.Errorf("action view identity or public shell mismatch")
				}
				parsed := win.Get("DOMParser").New().Call("parseFromString", data.HTML, "text/html")
				if _, e = streamBoundaryNodes(parsed.Get("body")); e != nil {
					return e
				}
				refresh := viewTarget == owner.source
				if e = commitNavigation(viewCtx, viewTarget, refresh, owner.prepareCommitState(refresh), owner.navigation, NavigationOptions{}, data); e != nil {
					return e
				}
				outcome.Applied = true
				outcome.Status = data.Status
				if e = owner.afterCommit(); e != nil {
					return e
				}
				stream, e = newNavigationStream(owner.navigation, data)
				if e != nil {
					return e
				}
				activeNavigationStream = stream
			case "chunk":
				return stream.apply(navigation.StreamFrame{ID: frame.ID, HTML: frame.HTML, Failed: frame.Failed})
			case "done":
				if stream == nil || len(stream.pending) != 0 {
					return fmt.Errorf("action view finished with pending boundaries")
				}
				if stream.failed {
					return fmt.Errorf("action view contains failed deferred content")
				}
			}
			return nil
		})
	})
	if err != nil {
		return outcome, err
	}
	if !streaming {
		response.Body = buffered
		if err = validate(response); err != nil {
			return outcome, err
		}
		confirmed = true
		return outcome, fmt.Errorf("action confirmed without a negotiated fresh view")
	}
	if err = decoder.Finish(); err != nil {
		return outcome, err
	}
	if err = owner.check(); err != nil {
		return outcome, err
	}
	if !confirmed {
		return outcome, errors.New("action response did not confirm a result")
	}
	return outcome, nil
}
