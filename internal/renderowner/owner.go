// Package renderowner records constructor origins only during one final,
// eligible buffered render. It is inert for stream/shell/capture writers.
package renderowner

import (
	"context"
	"github.com/Suhaibinator/Next.go/internal/navigation"
)

type Frame struct{ Kind, ID, Tag string }
type state struct {
	recorder *Recorder
	owner    int
}
type contextKey struct{}

func Semantic(kind string, fields ...string) Frame {
	return Frame{Kind: kind, ID: navigation.MergeIdentity("nextgo-merge-origin-v1", append([]string{kind}, fields...)...)}
}
func RecorderFrom(ctx context.Context) *Recorder {
	value, _ := ctx.Value(contextKey{}).(state)
	if value.recorder == nil || value.recorder.disabled {
		return nil
	}
	return value.recorder
}

// WithoutRecorder hides only this render's owner recorder from a child render.
// Fallback contents belong to the browser boundary, not the server owner's
// ancestry. Keep cancellation, deadlines and every unrelated render context;
// never disable the shared outer recorder needed by sibling island roots.
func WithoutRecorder(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, state{})
}
func With(ctx context.Context, frame Frame) context.Context {
	value, _ := ctx.Value(contextKey{}).(state)
	if value.recorder == nil || value.recorder.disabled {
		return ctx
	}
	if frame.Kind == "" {
		value.recorder.Disable()
		return ctx
	}
	owner := navigation.MergeOwner{Parent: value.owner, Kind: frame.Kind, ID: frame.ID, Tag: frame.Tag}
	if len(value.recorder.owners) >= navigation.MaxMergeOwners {
		value.recorder.Disable()
		return ctx
	}
	value.recorder.owners = append(value.recorder.owners, owner)
	value.owner = len(value.recorder.owners)
	return context.WithValue(ctx, contextKey{}, value)
}
