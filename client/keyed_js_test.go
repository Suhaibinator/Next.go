//go:build js && wasm

package client

import (
	"context"
	"github.com/Suhaibinator/Next.go/ui"
	"io"
	"strings"
	"testing"
)

func TestKeyedHTMLBudgetIncludesStringWrites(t *testing.T) {
	var output keyedHTML
	if err := ui.RenderTo(context.Background(), &output, ui.Text(strings.Repeat("x", 64<<10))); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(&output, "x"); err == nil || output.Len() != 64<<10 {
		t.Fatal("string writer escaped bound", err, output.Len())
	}
}
func TestKeyedRejectsClosedAndInvalidScopesWithoutDOM(t *testing.T) {
	options := KeyedOptions[int]{Key: func(int) string { return "x" }, View: func(int) ui.Node { return nil }, Bind: func(*Scope, int) (func(int), error) { return nil, nil }}
	if _, err := HydrateKeyed[int](nil, Element{}, nil, options); err != ErrKeyedClosed {
		t.Fatal(err)
	}
	scope := &Scope{closed: true}
	if _, err := HydrateKeyed(scope, Element{}, nil, options); err != ErrKeyedClosed {
		t.Fatal(err)
	}
	var list *KeyedList[int]
	if err := list.Set(nil); err != ErrKeyedClosed {
		t.Fatal(err)
	}
}
