//go:build !js

package preview

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/examples/form-validation/model"
)

func post(kind, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://example.test/api/preview", strings.NewReader(body))
	r.Header.Set("Content-Type", kind)
	w := httptest.NewRecorder()
	Handle(w, r)
	return w
}

func TestNativeChannelPresenceAndRepeatedValueReceipt(t *testing.T) {
	for _, selected := range []bool{false, true} {
		values := url.Values{"name": {"Ada"}, "code": {"GO"}, "topics": {"notes", "notes", ""}, "intent": {"native"}}
		if selected {
			values["channel"] = []string{""}
		}
		w := post("application/x-www-form-urlencoded", values.Encode())
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Body.String())
		}
		present := "false"
		summary := "No selection"
		if selected {
			present = "true"
			summary = "Selected empty value"
		}
		if !strings.Contains(w.Body.String(), `data-present="`+present+`"`) || !strings.Contains(w.Body.String(), summary) || !strings.Contains(w.Body.String(), `[&#34;notes&#34;,&#34;notes&#34;,&#34;&#34;]`) {
			t.Fatal(w.Body.String())
		}
		if strings.Contains(w.Body.String(), "data-fv-confirmed-indices") {
			t.Fatal("native values must not pretend to identify equal-valued options")
		}
	}
	values := url.Values{"name": {`<script>alert(1)</script>`}, "code": {"GO"}}
	w := post("application/x-www-form-urlencoded", values.Encode())
	if w.Code != 200 || strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestJSONPreservesExactOptionIdentityAndValidates(t *testing.T) {
	v := model.Initial(true)
	v.Topics = []int{1}
	v.Code = "go"
	encoded, _ := json.Marshal(v)
	w := post("application/json", string(encoded))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result model.PreviewResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Value.Topics) != 1 || result.Value.Topics[0] != 1 || !result.Value.Channel.Selected || result.Value.Channel.Value != "" || result.Value.Code != "GO" {
		t.Fatal(result)
	}
	v.Name = "reserved"
	encoded, _ = json.Marshal(v)
	w = post("application/json", string(encoded))
	if w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Fields["name"] != "This public name is reserved." {
		t.Fatal(result, err)
	}
	v.Name = "offline"
	encoded, _ = json.Marshal(v)
	w = post("application/json", string(encoded))
	if w.Code != 503 || strings.Contains(w.Body.String(), "database") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestPreviewRejectsAmbiguousOrOversizedPayloads(t *testing.T) {
	for _, body := range []string{"name=Ada&name=Lin&code=GO", "name=Ada&code=GO&channel=&channel=email", "name=Ada&code=GO&topics=notes&topics=notes&topics=notes", "name=Ada&code=GO&unknown=1"} {
		if w := post("application/x-www-form-urlencoded", body); w.Code != 400 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"name":"Ada","name":"Lin","code":"GO"}`, `{"unknown":true}`, strings.Repeat("x", maxBody+1)} {
		if w := post("application/json", body); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	Handle(w, httptest.NewRequest("GET", "http://example.test/api/preview", nil))
	if w.Code != 405 || w.Header().Get("Allow") != "POST" {
		t.Fatal(w.Code, w.Header())
	}
}
