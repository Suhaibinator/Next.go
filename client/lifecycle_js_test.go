//go:build js && wasm

package client

import (
	"maps"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

func TestDraftRestoreRequiresExactSessionAndRuntimeGeneration(t *testing.T) {
	oldDoc := doc
	attrs := map[string]string{"data-nextgo-draft": "1", "data-nextgo-build": "build", "data-nextgo-configuration": "configuration"}
	get := js.FuncOf(func(_ js.Value, args []js.Value) any { return attrs[args[0].String()] })
	body := js.ValueOf(map[string]any{"getAttribute": get})
	doc = js.ValueOf(map[string]any{"body": body})
	t.Cleanup(func() { doc = oldDoc; get.Release() })
	url := "https://site.test/demo/_nextgo/draft"
	valid := Response{StatusCode: 204, URL: url, Header: Headers{navigation.ProtocolHeader: {"3"}, navigation.ConfigurationHeader: {"configuration"}, navigation.BuildHeader: {"build"}, navigation.DraftHeader: {"1"}}}
	if !validDraftProbe(valid, url) {
		t.Fatal("valid restore rejected")
	}
	for _, change := range []func(*Response){
		func(r *Response) { r.StatusCode = 200 }, func(r *Response) { r.Body = []byte("unexpected") }, func(r *Response) { r.URL = "https://foreign.test/" },
		func(r *Response) { delete(r.Header, navigation.ProtocolHeader) }, func(r *Response) { r.Header[navigation.ConfigurationHeader] = []string{"changed"} }, func(r *Response) { r.Header[navigation.BuildHeader] = []string{"changed"} }, func(r *Response) { r.Header[navigation.DraftHeader] = []string{"0"} },
	} {
		r := valid
		r.Header = maps.Clone(valid.Header)
		change(&r)
		if validDraftProbe(r, url) {
			t.Fatal("incompatible restore accepted", r)
		}
	}
}
