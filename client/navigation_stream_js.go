//go:build js && wasm

package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall/js"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

var streamShellCommittedID uint64
var activeNavigationStream *navigationStream
var streamRetry *Scope
var errStreamFallback = errors.New("navigation requires a full document")

type committedStreamError struct{}

func (*committedStreamError) Error() string { return "streamed content was interrupted" }

type navigationStream struct {
	id      uint64
	pending map[string]js.Value
	known   map[string]bool
	failed  bool
}

func clearStreamRetry() {
	if streamRetry != nil {
		streamRetry.dispose()
		streamRetry = nil
	}
}
func showStreamRetry() {
	clearStreamRetry()
	status := doc.Call("getElementById", "nextgo-status")
	status.Set("textContent", "Some content could not finish loading. ")
	status.Set("hidden", false)
	button := doc.Call("createElement", "button")
	button.Set("type", "button")
	button.Set("textContent", "Retry loading content")
	button.Call("setAttribute", "data-nextgo-stream-retry", "")
	status.Call("appendChild", button)
	streamRetry = &Scope{Root: Element{status}}
	streamRetry.On(Element{button}, "click", func(Event) { Refresh() })
}
func (s *navigationStream) failPending() {
	if s == nil {
		return
	}
	for _, boundary := range s.pending {
		if boundary.Get("isConnected").Bool() {
			disposeWithin(boundary)
			boundary.Set("textContent", "Content loading was interrupted.")
			boundary.Call("removeAttribute", "aria-busy")
			boundary.Call("setAttribute", "data-nextgo-stream-error", "true")
			boundary.Call("setAttribute", "role", "alert")
		}
	}
	s.pending = map[string]js.Value{}
}
func stopNavigationStream() {
	if activeNavigationStream != nil {
		activeNavigationStream.failPending()
		activeNavigationStream = nil
	}
}

func streamBoundaryNodes(root js.Value) (map[string]js.Value, error) {
	result := map[string]js.Value{}
	nodes := root.Call("querySelectorAll", "[data-nextgo-boundary]")
	if nodes.Length() > 128 {
		return nil, fmt.Errorf("too many streamed boundaries")
	}
	for i := 0; i < nodes.Length(); i++ {
		node := nodes.Index(i)
		id := node.Call("getAttribute", "data-nextgo-boundary").String()
		if !navigation.ValidStreamID(id) || result[id].Truthy() {
			return nil, fmt.Errorf("invalid or duplicate stream boundary")
		}
		result[id] = node
	}
	return result, nil
}
func newNavigationStream(id uint64, data envelope) (*navigationStream, error) {
	parsed := win.Get("DOMParser").New().Call("parseFromString", data.HTML, "text/html")
	declared, err := streamBoundaryNodes(parsed.Get("body"))
	if err != nil {
		return nil, err
	}
	live, err := streamBoundaryNodes(doc.Call("getElementById", "nextgo-app"))
	if err != nil {
		return nil, err
	}
	stream := &navigationStream{id: id, pending: map[string]js.Value{}, known: map[string]bool{}}
	for name := range declared {
		node, ok := live[name]
		if !ok {
			return nil, fmt.Errorf("stream boundary missing after shell commit")
		}
		stream.pending[name] = node
		stream.known[name] = true
		node.Call("setAttribute", "aria-busy", "true")
		if node.Call("hasAttribute", "data-nextgo-stream-error").Bool() {
			node.Call("removeAttribute", "role")
			node.Call("removeAttribute", "data-nextgo-stream-error")
		}
	}
	return stream, nil
}

const streamOwnedNodes = `script,style,base,link,meta,title,nextgo-layout,nextgo-outlet,nextgo-slot,nextgo-template,[data-nextgo-selection],[data-nextgo-slot],#nextgo-app,#nextgo-status`

func (s *navigationStream) apply(frame navigation.StreamFrame) error {
	boundary, ok := s.pending[frame.ID]
	if !ok || !boundary.Get("isConnected").Bool() {
		return fmt.Errorf("unknown or completed stream boundary")
	}
	parsed := win.Get("DOMParser").New().Call("parseFromString", frame.HTML, "text/html")
	if parsed.Call("querySelector", streamOwnedNodes).Truthy() || boundary.Call("querySelector", streamOwnedNodes).Truthy() {
		return fmt.Errorf("stream boundary changes document or route ownership")
	}
	if _, err := scriptMarkers(parsed.Get("body")); err != nil {
		return err
	}
	children, err := streamBoundaryNodes(parsed.Get("body"))
	if err != nil {
		return err
	}
	if len(s.known)+len(children) > 128 {
		return fmt.Errorf("stream boundary count exceeded")
	}
	for name := range children {
		if s.known[name] {
			return fmt.Errorf("duplicate stream boundary ID")
		}
	}
	fragment := doc.Call("createDocumentFragment")
	body := parsed.Get("body")
	for body.Get("firstChild").Truthy() {
		fragment.Call("appendChild", body.Get("firstChild"))
	}
	disposeWithin(boundary)
	boundary.Call("replaceChildren", fragment)
	boundary.Call("removeAttribute", "aria-busy")
	delete(s.pending, frame.ID)
	if frame.Failed {
		s.failed = true
		boundary.Call("setAttribute", "data-nextgo-stream-error", "true")
	} else {
		boundary.Call("removeAttribute", "data-nextgo-stream-error")
	}
	for name, node := range children {
		s.known[name] = true
		s.pending[name] = node
	}
	hydrate()
	return nil
}

func fetchActiveNavigation(ctx context.Context, key, target string, pop bool, state js.Value, id uint64, options NavigationOptions, hint *shellResumeHint) (data envelope, streamed, fallback bool, err error) {
	if staticExport() {
		data, _, fallback, err = fetchEnvelope(ctx, key)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	started := time.Now()
	headers := map[string]string{"Cache-Control": "no-cache", "X-Nextgo-Navigate": "1", navigation.StreamHeader: fmt.Sprint(navigation.StreamVersion)}
	headers[navigation.ShellProtocolHeader] = fmt.Sprint(navigation.ShellVersion)
	if hint != nil && hint.epoch == cacheEpoch {
		headers[navigation.ShellHeader] = hint.page.Shell
	}
	if routeManifest.Parallel != "" {
		var selected string
		selected, err = slotRequestState(ctx)
		if err != nil {
			return
		}
		headers[navigation.SlotsHeader] = selected
	}
	var response Response
	var buffered []byte
	var decoder navigation.StreamDecoder
	var session *navigationStream
	isStream := false
	defer func() {
		if err != nil && hint != nil && !streamed {
			discardShellHint(key)
		}
		if session != nil && activeNavigationStream == session {
			activeNavigationStream = nil
		}
		if streamed && err != nil {
			session.failPending()
			err = &committedStreamError{}
		}
	}()
	_, err = FetchStream(ctx, key, FetchOptions{Headers: headers, Cache: "no-store"}, func(value Response) error {
		response = value
		if !sameOriginResponse(value.URL) {
			if sameResponseOrigin(value.URL) {
				return errStreamFallback
			}
			return fmt.Errorf("navigation redirected outside the application origin")
		}
		if value.Header.Get(navigation.DraftHeader) != documentDraft() || value.Header.Get(navigation.ProtocolHeader) != fmt.Sprint(navigation.ProtocolVersion) {
			return errStreamFallback
		}
		media := strings.ToLower(strings.TrimSpace(strings.Split(value.Header.Get("Content-Type"), ";")[0]))
		isStream = media == navigation.StreamMediaType
		if isStream && (value.StatusCode != 200 || value.Header.Get(navigation.StreamHeader) != fmt.Sprint(navigation.StreamVersion) || value.Header.Get(navigation.ConfigurationHeader) != documentConfiguration()) {
			return errStreamFallback
		}
		if !isStream && media != "application/json" {
			return errStreamFallback
		}
		return nil
	}, func(chunk []byte) error {
		if id != navigationID || ctx.Err() != nil {
			return context.Canceled
		}
		if !isStream {
			buffered = append(buffered, chunk...)
			return nil
		}
		return decoder.Feed(chunk, func(frame navigation.StreamFrame) error {
			if id != navigationID || ctx.Err() != nil {
				return context.Canceled
			}
			switch frame.Type {
			case "shell":
				var replaceDocument bool
				data, _, replaceDocument, err = decodeEnvelopeResponseWithShell(response, frame.Page, key, started, true, shellDecodeOptions{hint: hint})
				if err != nil {
					return err
				}
				if replaceDocument {
					return errStreamFallback
				}
				parsed := win.Get("DOMParser").New().Call("parseFromString", data.HTML, "text/html")
				if _, e := streamBoundaryNodes(parsed.Get("body")); e != nil {
					return e
				}
				if e := commitNavigation(ctx, target, pop, state, id, options, data); e != nil {
					return e
				}
				streamed = true
				if id != navigationID || ctx.Err() != nil {
					return context.Canceled
				}
				streamShellCommittedID = id
				session, err = newNavigationStream(id, data)
				if err != nil {
					return err
				}
				activeNavigationStream = session
				doc.Call("getElementById", "nextgo-app").Call("removeAttribute", "aria-busy")
				status := doc.Call("getElementById", "nextgo-status")
				status.Set("textContent", "Loading content…")
				status.Set("hidden", false)
			case "chunk":
				if session == nil {
					return fmt.Errorf("stream boundary precedes shell")
				}
				return session.apply(frame)
			case "done":
				if session == nil || len(session.pending) != 0 {
					return fmt.Errorf("stream completed with unfinished boundaries")
				}
			}
			return nil
		})
	})
	if errors.Is(err, errStreamFallback) && !streamed {
		fallback = true
		err = nil
		invalidateCache()
		return
	}
	if err != nil {
		return
	}
	if isStream {
		if err = decoder.Finish(); err != nil {
			return
		}
		if session.failed {
			err = &committedStreamError{}
			return
		}
		doc.Call("getElementById", "nextgo-status").Set("hidden", true)
		return
	}
	data, _, fallback, err = decodeEnvelopeResponse(response, buffered, key, started, false)
	return
}
