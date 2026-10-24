package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recordingStream struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	flushes chan string
}

func newRecordingStream() *recordingStream { return &recordingStream{flushes: make(chan string, 16)} }
func (w *recordingStream) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(data)
}
func (w *recordingStream) Flush() {
	w.mu.Lock()
	snapshot := w.buf.String()
	w.mu.Unlock()
	w.flushes <- snapshot
}
func (w *recordingStream) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.buf.String() }
func awaitFlush(t *testing.T, w *recordingStream) string {
	t.Helper()
	select {
	case snapshot := <-w.flushes:
		return snapshot
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not flush")
		return ""
	}
}
func awaitRender(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not finish")
		return nil
	}
}

func TestDeferOrdinarySSR(t *testing.T) {
	called := false
	result, err := HTML(Fragment(Text("before"), Defer("content", Text("loading"), func(ctx context.Context) (Node, error) {
		called = true
		if ctx == nil {
			t.Error("missing loader context")
		}
		return El("p", nil, Text("loaded <content>")), nil
	}), Text("after")))
	if err != nil || !called || result != "before<p>loaded &lt;content&gt;</p>after" {
		t.Fatalf("%q %v called=%v", result, err, called)
	}
	failure := errors.New("loader failure")
	if _, err := HTML(Defer("content", nil, func(context.Context) (Node, error) { return nil, failure })); !errors.Is(err, failure) {
		t.Fatalf("ordinary SSR lost loader error: %v", err)
	}
}

func TestRenderStreamProgressiveChunks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	first, second := make(chan struct{}), make(chan struct{})
	loader := func(release <-chan struct{}, text string) func(context.Context) (Node, error) {
		return func(ctx context.Context) (Node, error) {
			select {
			case <-release:
				return El("p", nil, Text(text)), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	w := newRecordingStream()
	done := make(chan error, 1)
	go func() {
		done <- RenderStream(ctx, w, Fragment(Text("before"), Defer("first", Text("loading first"), loader(first, "first <loaded>")), Defer("second", Text("loading second"), loader(second, "second loaded")), Text("after")))
	}()
	initial := awaitFlush(t, w)
	if initial != `before<div aria-busy="true" data-nextgo-boundary="first">loading first</div><div aria-busy="true" data-nextgo-boundary="second">loading second</div>after` {
		t.Fatalf("incomplete initial render: %s", initial)
	}
	close(second)
	next := awaitFlush(t, w)
	if !strings.HasSuffix(next, `<template data-nextgo-chunk="second"><p>second loaded</p></template>`) || strings.Contains(next, `data-nextgo-chunk="first"`) {
		t.Fatalf("second boundary did not finish first: %s", next)
	}
	close(first)
	last := awaitFlush(t, w)
	if !strings.HasSuffix(last, `<template data-nextgo-chunk="first"><p>first &lt;loaded&gt;</p></template>`) {
		t.Fatalf("bad final chunk: %s", last)
	}
	if err := awaitRender(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestRenderStreamErrorsStayGeneric(t *testing.T) {
	for name, loader := range map[string]func(context.Context) (Node, error){
		"loader": func(context.Context) (Node, error) { return nil, errors.New("secret database credentials") },
		"panic":  func(context.Context) (Node, error) { panic("secret loader panic") },
		"render": func(context.Context) (Node, error) {
			return El("a", Attrs{"href": "javascript:secret()"}, Text("secret partial content")), nil
		},
		"render-panic": func(context.Context) (Node, error) { return streamPanicNode{}, nil },
	} {
		t.Run(name, func(t *testing.T) {
			var w bytes.Buffer
			if err := RenderStream(context.Background(), &w, Defer("content", Text("loading"), loader)); err != nil {
				t.Fatal(err)
			}
			result := w.String()
			if strings.Contains(result, "secret") || !strings.Contains(result, `<template data-nextgo-chunk="content" data-nextgo-stream-error="true">`) || !strings.Contains(result, `role="alert">Unable to load content.`) {
				t.Fatalf("unsafe error chunk: %s", result)
			}
		})
	}
}

type streamPanicNode struct{}

func (streamPanicNode) Render(w io.Writer) error {
	_, _ = io.WriteString(w, "secret partial render")
	panic("secret panic")
}

func TestRenderStreamCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newRecordingStream()
	started := make(chan struct{})
	stopped := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- RenderStream(ctx, w, Defer("waiting", Text("loading"), func(ctx context.Context) (Node, error) {
			close(started)
			<-ctx.Done()
			close(stopped)
			return nil, ctx.Err()
		}))
	}()
	awaitFlush(t, w)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("loader did not start")
	}
	cancel()
	if err := awaitRender(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("loader context not canceled")
	}
	if strings.Contains(w.String(), "<template") {
		t.Fatal("canceled stream emitted a chunk")
	}
	var untouched bytes.Buffer
	if err := RenderStream(ctx, &untouched, Text("should not render")); !errors.Is(err, context.Canceled) || untouched.Len() != 0 {
		t.Fatalf("already-canceled stream: %q %v", untouched.String(), err)
	}
}

func TestRenderStreamValidation(t *testing.T) {
	load := func(context.Context) (Node, error) { return Text("loaded"), nil }
	for _, id := range []string{"", "has space", `bad"id`, "9first", "has/slash", "日本語", "<script>"} {
		if _, err := HTML(Defer(id, nil, load)); err == nil {
			t.Errorf("ordinary render accepted invalid ID %q", id)
		}
		var w bytes.Buffer
		if err := RenderStream(context.Background(), &w, Defer(id, nil, load)); err == nil || w.Len() != 0 {
			t.Errorf("stream accepted invalid ID %q: %q %v", id, w.String(), err)
		}
	}
	for _, node := range []Node{
		Fragment(Defer("same", Text("one"), load), Defer("same", Text("two"), load)),
		Defer("nilLoader", Text("loading"), nil),
		Fragment(Text("partial initial content"), El("script", nil)),
		streamPanicNode{},
	} {
		var w bytes.Buffer
		if err := RenderStream(context.Background(), &w, node); err == nil || w.Len() != 0 {
			t.Fatalf("invalid initial tree leaked content: %q %v", w.String(), err)
		}
	}
}

func TestRenderStreamNestedDeferredStreamsAfterParent(t *testing.T) {
	type contextKey struct{}
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), contextKey{}, "request value"), 3*time.Second)
	defer cancel()
	var w bytes.Buffer
	outer := Defer("outer", Text("outer loading"), func(context.Context) (Node, error) {
		return Fragment(Text("outer content "), Defer("inner", Text("inner loading"), func(innerCtx context.Context) (Node, error) {
			if innerCtx.Value(contextKey{}) != "request value" {
				return nil, errors.New("nested loader lost stream context")
			}
			return Text("inner content"), nil
		})), nil
	})
	if err := RenderStream(ctx, &w, outer); err != nil {
		t.Fatal(err)
	}
	want := `<div aria-busy="true" data-nextgo-boundary="outer">outer loading</div><template data-nextgo-chunk="outer">outer content <div aria-busy="true" data-nextgo-boundary="inner">inner loading</div></template><template data-nextgo-chunk="inner">inner content</template>`
	if w.String() != want {
		t.Fatalf("nested boundary was not resolved inside chunk: %s", w.String())
	}
}

type shortStreamWriter struct{ started <-chan struct{} }

func (w shortStreamWriter) Write(p []byte) (int, error) {
	if w.started != nil {
		<-w.started
	}
	if len(p) == 0 {
		return 0, nil
	}
	return len(p) - 1, nil
}
func TestRenderStreamWriterErrorCancelsLoaders(t *testing.T) {
	stopped := make(chan struct{})
	started := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := RenderStream(ctx, shortStreamWriter{started: started}, Defer("waiting", Text("loading"), func(ctx context.Context) (Node, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	}))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write error lost: %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("writer error did not cancel running loader")
	}
}

func TestBufferedContextFlowsThroughNestedDeferredNodes(t *testing.T) {
	type contextKey struct{}
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), contextKey{}, "request-value"), time.Second)
	defer cancel()
	calls := 0
	check := func(ctx context.Context) {
		t.Helper()
		calls++
		if ctx.Value(contextKey{}) != "request-value" {
			t.Error("lost request values")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("lost request deadline")
		}
	}
	node := El("main", nil, Fragment(Defer("outer", nil, func(ctx context.Context) (Node, error) {
		check(ctx)
		return El("section", nil, Defer("inner", nil, func(ctx context.Context) (Node, error) { check(ctx); return Text("complete"), nil })), nil
	})))
	html, err := HTMLContext(ctx, node)
	if err != nil || html != "<main><section>complete</section></main>" || calls != 2 {
		t.Fatalf("%s %v calls=%d", html, err, calls)
	}
}

func TestBufferedDeferredContextCancelsWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := HTMLContext(ctx, Defer("slow", nil, func(ctx context.Context) (Node, error) { close(started); <-ctx.Done(); return nil, ctx.Err() }))
		result <- err
	}()
	<-started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("buffered loader ignored request cancellation")
	}
	if _, err := HTMLContext(nil, Text("no context")); err == nil {
		t.Fatal("nil context accepted")
	}
}

func TestRenderEventsNestedBoundaryDoesNotBlockSibling(t *testing.T) {
	outer := make(chan struct{})
	inner := make(chan struct{})
	sibling := make(chan struct{})
	events := make(chan StreamEvent, 8)
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		done <- RenderEvents(ctx, Fragment(Defer("parent", Text("parent fallback"), func(context.Context) (Node, error) {
			<-outer
			return Defer("child", Text("child fallback"), func(context.Context) (Node, error) { <-inner; return Text("child complete"), nil }), nil
		}), Defer("sibling", Text("sibling fallback"), func(context.Context) (Node, error) { <-sibling; return Text("sibling complete"), nil })), func(event StreamEvent) error { events <- event; return nil })
	}()
	next := func() StreamEvent {
		t.Helper()
		select {
		case event := <-events:
			return event
		case <-ctx.Done():
			t.Fatal("missing event")
			return StreamEvent{}
		}
	}
	if next().ID != "" {
		t.Fatal("missing shell")
	}
	close(outer)
	if event := next(); event.ID != "parent" || !bytes.Contains(event.HTML, []byte("child fallback")) {
		t.Fatal(event.ID)
	}
	close(sibling)
	if next().ID != "sibling" {
		t.Fatal("sibling waited for nested work")
	}
	close(inner)
	if next().ID != "child" {
		t.Fatal("nested content missing")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestRenderEventsResourceAndFallbackLimits(t *testing.T) {
	loader := func(context.Context) (Node, error) { return Text("ok"), nil }
	var children []Node
	for i := 0; i < MaxStreamBoundaries+1; i++ {
		children = append(children, Defer(fmt.Sprintf("item%d", i), nil, loader))
	}
	for _, node := range []Node{Fragment(children...), Text(strings.Repeat("x", MaxStreamChunkBytes+1)), Defer("outer", Defer("invalid-fallback", nil, loader), loader)} {
		count := 0
		if err := RenderEvents(context.Background(), node, func(StreamEvent) error { count++; return nil }); err == nil || count != 0 {
			t.Fatal("invalid initial tree was emitted", err, count)
		}
	}
}

func TestRenderEventsBoundsConcurrentLoaders(t *testing.T) {
	gate, saturated := make(chan struct{}), make(chan struct{})
	var active, peak, started atomic.Int32
	var once sync.Once
	var nodes []Node
	for i := 0; i < 32; i++ {
		nodes = append(nodes, Defer(fmt.Sprintf("job%d", i), nil, func(ctx context.Context) (Node, error) {
			n := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); n > old; old = peak.Load() {
				if peak.CompareAndSwap(old, n) {
					break
				}
			}
			if started.Add(1) == MaxStreamConcurrency {
				once.Do(func() { close(saturated) })
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-gate:
				return Text("ready"), nil
			}
		}))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RenderEvents(ctx, Fragment(nodes...), func(StreamEvent) error { return nil }) }()
	select {
	case <-saturated:
	case <-ctx.Done():
		t.Fatal("loader pool did not start")
	}
	if started.Load() != MaxStreamConcurrency {
		t.Fatal("too many callbacks started before admission", started.Load())
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if peak.Load() > MaxStreamConcurrency {
		t.Fatal("unbounded concurrent callbacks", peak.Load())
	}
}
