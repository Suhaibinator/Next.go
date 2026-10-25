package mutation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/actionwire"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

func decode[O any](status int, protocol, revision, want string, body []byte) (Response[O], error) {
	var result Response[O]
	data, redirect, err := decodeEnvelope(status, protocol, revision, want, body)
	if err != nil {
		return result, err
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		switch reflect.TypeFor[O]().Kind() {
		case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice:
		default:
			return result, badEnvelope("null result for a nonnullable output")
		}
	}
	if err := strictJSON(data, &result.Data); err != nil {
		return result, badEnvelope("result does not match output type")
	}
	if redirect != "" {
		if _, err := (paths.Config{}).URL(redirect); err != nil {
			return result, badEnvelope("unsafe redirect metadata")
		}
	}
	result.Redirect = redirect
	return result, nil
}
func badEnvelope(reason string) error { return fmt.Errorf("%w: %s", ErrProtocol, reason) }
func decodeEnvelope(status int, protocol, revision, want string, body []byte) (json.RawMessage, string, error) {
	bad := func(reason string) (json.RawMessage, string, error) { return nil, "", badEnvelope(reason) }
	if protocol != actionwire.Protocol {
		return bad("missing or unsupported protocol")
	}
	if revision != "" && revision != want {
		return bad("contract revision changed")
	}
	if err := uniqueJSON(body); err != nil {
		return bad("invalid JSON envelope")
	}
	var envelope struct {
		Data     json.RawMessage `json:"data"`
		Redirect string          `json:"redirect"`
		Error    *struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Fields  map[string]string `json:"fields"`
		} `json:"error"`
	}
	if err := strictJSON(body, &envelope); err != nil {
		return bad("invalid response envelope")
	}
	if status >= 400 && status <= 599 {
		if envelope.Error == nil || envelope.Error.Code == "" || envelope.Error.Message == "" || envelope.Data != nil || envelope.Redirect != "" {
			return bad("invalid failure envelope")
		}
		return nil, "", &Error{Status: status, Code: envelope.Error.Code, Message: envelope.Error.Message, Fields: envelope.Error.Fields}
	}
	if status != 200 || revision != want || envelope.Error != nil || envelope.Data == nil {
		return bad("invalid success envelope")
	}
	return envelope.Data, envelope.Redirect, nil
}

func strictJSON(data []byte, dst any) error { return wirejson.Strict(data, dst) }
func uniqueJSON(data []byte) error          { return wirejson.Unique(data) }

func jsonMediaType(value string) bool {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	return value == "application/json"
}
