// Package mutation provides shared typed contracts for native action handlers
// and Go/WASM callers. Bind is native-only; Call is browser-only.
package mutation

import (
	"errors"
	"fmt"
	"github.com/Suhaibinator/Next.go/internal/actionwire"
)

var ErrProtocol = errors.New("nextgo: incompatible mutation response")

// Contract identifies an explicitly versioned input/output schema. Names and
// versions are public endpoint identifiers, never authentication capabilities.
type Contract[I, O any] struct{ name, revision string }

func New[I, O any](name, revision string) (Contract[I, O], error) {
	if len(name) > 256 || len(revision) > 64 || !actionwire.ValidName(name) || !actionwire.ValidSegment(revision) {
		return Contract[I, O]{}, fmt.Errorf("nextgo: invalid mutation name or revision")
	}
	return Contract[I, O]{name: name, revision: revision}, nil
}
func Must[I, O any](name, revision string) Contract[I, O] {
	c, err := New[I, O](name, revision)
	if err != nil {
		panic(err)
	}
	return c
}
func (c Contract[I, O]) Name() string     { return c.name }
func (c Contract[I, O]) Revision() string { return c.revision }
func (c Contract[I, O]) Key() string      { return c.name + "/" + c.revision }

// Path is application-relative. Resolve it with nextgo.URL/client.URL to apply
// deployment and locale prefixes.
func (c Contract[I, O]) Path() string    { return "/_nextgo/actions/" + c.Key() }
func (c Contract[I, O]) validate() error { _, err := New[I, O](c.name, c.revision); return err }

// Response carries typed data and an optional local redirect suggestion. Call
// does not navigate automatically; callers coordinate UI and navigation.
type Response[O any] struct {
	Data     O
	Redirect string
}

// Error is a public server failure. Field messages are separate from the typed
// success value. Transport/protocol failures do not masquerade as this type.
type Error struct {
	Status  int
	Code    string
	Message string
	Fields  map[string]string
}

func (e *Error) Error() string { return e.Message }
