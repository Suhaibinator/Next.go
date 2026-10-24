package nextgo

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func FuzzHeaderRuleCaptureCannotSplitResponse(f *testing.F) {
	for _, seed := range []string{"value", "café", "\r\nInjected: yes", "\x00", "\tallowed", "\x7f", string([]byte{255})} {
		f.Add(seed)
	}
	app, err := New(Config{HeaderRules: []HeaderRule{{Source: "/x", Has: []RuleCondition{{Type: HeaderCondition, Key: "X-Input", Value: `(?<input>[\s\S]*)`}}, Headers: []HeaderValue{{Name: "X-Output", Value: "prefix {{input}}"}}}}})
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { _ = app.Close(context.Background()) })
	f.Fuzz(func(t *testing.T, value string) {
		r := httptest.NewRequest("GET", "/x", nil)
		r.Header.Set("X-Input", value)
		output, sensitive, err := app.headerRuleValues(r)
		if !sensitive {
			t.Fatal("conditional path became public")
		}
		if err != nil {
			if len(output) != 0 {
				t.Fatal("partial header result on failure")
			}
			return
		}
		for _, header := range output {
			if strings.ContainsAny(header.Value, "\r\n\x00") || len(header.Value) > 8192 || header.Name != "X-Output" {
				t.Fatal("unsafe response field")
			}
		}
	})
}
