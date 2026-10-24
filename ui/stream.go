package ui

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"regexp"
)

var boundaryID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

type deferredNode struct {
	id       string
	fallback Node
	loader   func(context.Context) (Node, error)
}

// Defer describes a server-loaded streaming boundary. IDs must be unique within
// a stream and match [A-Za-z][A-Za-z0-9_-]*. RenderStream first emits fallback and
// later an inert template containing the loaded content. Ordinary rendering
// waits for the loader and emits only its final content, without a boundary.
// Nested deferred nodes in loaded content stream independently after their parent.
// A fallback must contain only immediate nodes.
func Defer(id string, fallback Node, loader func(context.Context) (Node, error)) Node {
	return &deferredNode{id: id, fallback: fallback, loader: loader}
}

func (d *deferredNode) Render(w io.Writer) error {
	if len(d.id) > 128 || !boundaryID.MatchString(d.id) {
		return fmt.Errorf("ui: invalid streaming boundary ID %q", d.id)
	}
	if d.loader == nil {
		return fmt.Errorf("ui: streaming boundary %q has no loader", d.id)
	}
	if stream, ok := w.(*streamWriter); ok {
		return stream.deferNode(d)
	}
	ctx := context.Background()
	if contextual, ok := w.(interface{ renderContext() context.Context }); ok {
		ctx = contextual.renderContext()
	}
	node, err := resolveDeferred(ctx, d.loader)
	if err != nil {
		return fmt.Errorf("ui: streaming boundary %q: %w", d.id, err)
	}
	if node == nil {
		return nil
	}
	return node.Render(w)
}

// Stream limits bound framework-owned buffers and scheduled loader work.
const MaxStreamBoundaries = 128
const MaxStreamConcurrency = 16
const MaxStreamDepth = 16
const MaxStreamChunkBytes = 2 << 20
const MaxStreamBytes = 8 << 20

var ErrStreamLimit = fmt.Errorf("ui: stream exceeds its resource limits")

// StreamEvent is a validated shell (empty ID) or a completed deferred boundary.
// Err is server-only diagnostic data; HTML contains a generic accessible error.
type StreamEvent struct {
	ID   string
	HTML []byte
	Err  error `json:"-"`
}
type streamTask struct {
	node  *deferredNode
	depth int
}
type streamResult struct {
	task streamTask
	node Node
	err  error
}
type streamWriter struct {
	contextWriter
	known        map[string]bool
	tasks        []streamTask
	depth, total int
	fallback     bool
}
type streamBuffer struct{ data bytes.Buffer }

func (b *streamBuffer) Write(p []byte) (int, error) {
	if len(p) > MaxStreamChunkBytes-b.data.Len() {
		return 0, ErrStreamLimit
	}
	return b.data.Write(p)
}
func (b *streamBuffer) WriteString(value string) (int, error) {
	if len(value) > MaxStreamChunkBytes-b.data.Len() {
		return 0, ErrStreamLimit
	}
	return b.data.WriteString(value)
}

type contextWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w *contextWriter) renderContext() context.Context { return w.ctx }
func (w *contextWriter) WriteString(value string) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return io.WriteString(w.w, value)
}
func (w *contextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.w.Write(p)
}
func (w *streamWriter) deferNode(d *deferredNode) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if w.fallback {
		return fmt.Errorf("ui: deferred fallbacks cannot contain deferred boundaries")
	}
	if len(d.id) > 128 || !boundaryID.MatchString(d.id) || d.loader == nil {
		return fmt.Errorf("ui: invalid deferred boundary")
	}
	if w.depth >= MaxStreamDepth || w.total+len(w.tasks) >= MaxStreamBoundaries {
		return ErrStreamLimit
	}
	if w.known[d.id] {
		return fmt.Errorf("ui: duplicate streaming boundary ID %q", d.id)
	}
	for _, task := range w.tasks {
		if task.node.id == d.id {
			return fmt.Errorf("ui: duplicate streaming boundary ID %q", d.id)
		}
	}
	w.fallback = true
	err := El("div", Attrs{"data-nextgo-boundary": d.id, "aria-busy": "true"}, d.fallback).Render(w)
	w.fallback = false
	if err != nil {
		return err
	}
	w.tasks = append(w.tasks, streamTask{d, w.depth + 1})
	return nil
}
func renderEventNode(ctx context.Context, node Node, known map[string]bool, depth, total int) (data []byte, tasks []streamTask, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			data = nil
			tasks = nil
			err = fmt.Errorf("ui: stream render panic: %v", failure)
		}
	}()
	buffer := &streamBuffer{}
	writer := &streamWriter{contextWriter: contextWriter{ctx, buffer}, known: known, depth: depth, total: total}
	if node != nil {
		if err := node.Render(writer); err != nil {
			return nil, nil, err
		}
	}
	return buffer.data.Bytes(), writer.tasks, nil
}

// RenderEvents emits a shell followed by independently resolved boundary events.
// Nested boundaries discovered in loaded content start after their parent event;
// fallbacks must be immediate nodes. IDs are unique across the whole stream.
// The emitter runs on the calling goroutine and provides backpressure. Loader
// callbacks must cooperate with cancellation; Go cannot forcibly stop callbacks.
func RenderEvents(ctx context.Context, node Node, emit func(StreamEvent) error) (err error) {
	if ctx == nil || emit == nil {
		return fmt.Errorf("ui: stream requires a context and emitter")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("ui: stream panic: %v", failure)
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	known := map[string]bool{}
	initial, tasks, err := renderEventNode(ctx, node, known, 0, 0)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, task := range tasks {
		known[task.node.id] = true
	}
	jobs := make(chan streamTask, MaxStreamBoundaries)
	results := make(chan streamResult)
	if len(tasks) > 0 {
		for i := 0; i < MaxStreamConcurrency; i++ {
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case task := <-jobs:
						node, err := resolveDeferred(ctx, task.node.loader)
						select {
						case results <- streamResult{task, node, err}:
						case <-ctx.Done():
							return
						}
					}
				}
			}()
		}
	}
	for _, task := range tasks {
		jobs <- task
	}
	if err := emit(StreamEvent{HTML: initial}); err != nil {
		return err
	}
	pending, total, used := len(tasks), len(tasks), len(initial)
	for pending > 0 {
		var result streamResult
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result = <-results:
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		pending--
		var data []byte
		var children []streamTask
		if result.err == nil {
			data, children, result.err = renderEventNode(ctx, result.node, known, result.task.depth, total)
		}
		if used+len(data) > MaxStreamBytes {
			result.err = ErrStreamLimit
		}
		if result.err != nil {
			data = []byte(`<div data-nextgo-stream-error="true" role="alert">Unable to load content.</div>`)
			children = nil
		}
		if used+len(data) > MaxStreamBytes {
			return ErrStreamLimit
		}
		used += len(data)
		if err := emit(StreamEvent{ID: result.task.node.id, HTML: data, Err: result.err}); err != nil {
			return err
		}
		for _, task := range children {
			known[task.node.id] = true
			jobs <- task
		}
		pending += len(children)
		total += len(children)
	}
	return ctx.Err()
}

func resolveDeferred(ctx context.Context, loader func(context.Context) (Node, error)) (node Node, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			node, err = nil, fmt.Errorf("ui: deferred loader panic: %v", failure)
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return loader(ctx)
}

// RenderStream writes inert HTML templates for the initial document consumer.
// Navigation uses RenderEvents with a separate versioned JSON transport.
func RenderStream(ctx context.Context, w io.Writer, node Node) error {
	return RenderEvents(ctx, node, func(event StreamEvent) error {
		data := event.HTML
		if event.ID != "" {
			attr := ""
			if event.Err != nil {
				attr = ` data-nextgo-stream-error="true"`
			}
			var chunk bytes.Buffer
			chunk.WriteString(`<template data-nextgo-chunk="` + html.EscapeString(event.ID) + `"` + attr + `>`)
			chunk.Write(data)
			chunk.WriteString(`</template><span hidden data-nextgo-chunk-ready="` + html.EscapeString(event.ID) + `"></span>`)
			data = chunk.Bytes()
		}
		if err := writeStreamBytes(ctx, w, data); err != nil {
			return err
		}
		if flusher, ok := w.(interface{ Flush() }); ok {
			flusher.Flush()
		}
		return nil
	})
}

func writeStreamBytes(ctx context.Context, w io.Writer, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}
