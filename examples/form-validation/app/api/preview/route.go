//go:build !js

package preview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/client"
	"github.com/Suhaibinator/Next.go/examples/form-validation/model"
	"github.com/Suhaibinator/Next.go/ui"
)

const maxBody = 4096

func Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		http.Error(w, "Unsupported Media Type", 415)
		return
	}
	if kind == "application/json" {
		handleJSON(w, r)
		return
	}
	if kind != "application/x-www-form-urlencoded" {
		http.Error(w, "Unsupported Media Type", 415)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", 400)
		return
	}
	value, topics, ok := nativeInput(r.PostForm)
	if !ok {
		http.Error(w, "Bad Request", 400)
		return
	}
	fields, err := model.PreviewFields(r.Context(), value)
	if r.Context().Err() != nil {
		return
	}
	if err != nil {
		http.Error(w, "Public check unavailable; retry explicitly.", 503)
		return
	}
	status := 200
	if len(fields) > 0 {
		status = 422
	}
	encoded, _ := json.Marshal(topics)
	errors := []ui.Node{}
	for _, field := range []string{"name", "code", "channel", "topics"} {
		if fields[field] != "" {
			errors = append(errors, ui.El("li", nil, ui.Text(field+": "+fields[field])))
		}
	}
	page := ui.El("html", ui.Attrs{"lang": "en"}, ui.El("head", nil, ui.El("title", nil, ui.Text("Native public form receipt"))), ui.El("body", nil, ui.El("main", ui.Attrs{"data-fv-receipt": ""}, ui.El("h1", nil, ui.Text("Native public preview")), ui.El("p", nil, ui.Text("Public synthetic input; nothing was saved.")), ui.El("ul", nil, errors...), ui.El("p", ui.Attrs{"data-fv-native-name": ""}, ui.Text(value.Name)), ui.El("p", ui.Attrs{"data-fv-native-code": ""}, ui.Text(value.Code)), ui.El("p", ui.Attrs{"data-fv-native-channel": "", "data-present": strconv.FormatBool(value.Channel.Selected)}, ui.Text(model.ChannelSummary(value.Channel))), ui.El("pre", ui.Attrs{"data-fv-native-values": ""}, ui.Text(string(encoded))), ui.El("p", nil, ui.Text("Repeated values describe the native submission. Equal-valued option identity is not inferred.")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(r.Context(), "/")}, ui.Text("Return to form previews")))))
	markup, err := ui.HTML(page)
	if err != nil {
		http.Error(w, "Internal Server Error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "<!doctype html>"+markup)
}

func nativeInput(values url.Values) (model.Values, []string, bool) {
	for name, items := range values {
		if name != "name" && name != "code" && name != "channel" && name != "topics" && name != "intent" {
			return model.Values{}, nil, false
		}
		if name != "topics" && len(items) != 1 {
			return model.Values{}, nil, false
		}
	}
	if len(values["name"]) != 1 || len(values["code"]) != 1 || values.Get("intent") != "" && values.Get("intent") != "native" {
		return model.Values{}, nil, false
	}
	v := model.Values{Name: values.Get("name"), Code: values.Get("code")}
	if channel, exists := values["channel"]; exists {
		v.Channel = client.RadioChoice{Selected: true, Value: channel[0]}
	}
	topics := append([]string{}, values["topics"]...)
	limits := map[string]int{"notes": 2, "events": 1, "": 1}
	for _, value := range topics {
		if limits[value] == 0 {
			return model.Values{}, nil, false
		}
		limits[value]--
	}
	return v, topics, true
}

func handleJSON(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad Request", 400)
		return
	}
	var value model.Values
	if decodePreviewJSON(data, &value) != nil {
		http.Error(w, "Bad Request", 400)
		return
	}
	fields, err := model.PreviewFields(r.Context(), value)
	if r.Context().Err() != nil {
		return
	}
	if err != nil {
		http.Error(w, "Public check unavailable; retry explicitly.", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	result := model.PreviewResult{Value: model.Normalize(value), Message: "Public preview confirmed. Nothing saved."}
	if len(fields) > 0 {
		w.WriteHeader(422)
		result.Fields = fields
		result.Message = "Review the highlighted public fields."
	}
	_ = json.NewEncoder(w).Encode(result)
}

// This sample keeps duplicate-key rejection local rather than depending on
// framework internals. The request has already been capped at 4 KiB. Limit JSON
// depth as well, then let the standard decoder check the public DTO's types.
func decodePreviewJSON(data []byte, value *model.Values) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("expected object")
	}
	scan := json.NewDecoder(bytes.NewReader(data))
	var unique func(int) error
	unique = func(depth int) error {
		if depth > 8 {
			return fmt.Errorf("JSON nesting limit")
		}
		token, err := scan.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for scan.More() {
				key, err := scan.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[strings.ToLower(name)] {
					return fmt.Errorf("duplicate or invalid key")
				}
				seen[strings.ToLower(name)] = true
				if err := unique(depth + 1); err != nil {
					return err
				}
			}
			end, err := scan.Token()
			if err != nil || end != json.Delim('}') {
				return fmt.Errorf("invalid object")
			}
		case json.Delim('['):
			for scan.More() {
				if err := unique(depth + 1); err != nil {
					return err
				}
			}
			end, err := scan.Token()
			if err != nil || end != json.Delim(']') {
				return fmt.Errorf("invalid array")
			}
		}
		return nil
	}
	if err := unique(0); err != nil {
		return err
	}
	if _, err := scan.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	return nil
}
