// Package offlinejson decodes bounded, closed offline protocol structures.
// It is separate from the portable offline policy so encoding/json and reflect
// enter only the native, document adapter or dedicated worker graphs that need
// a wire codec. It imports no application or offline policy package.
package offlinejson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

const MaxBytes = 256 << 10
const MaxDepth = 8

var ErrInvalid = errors.New("offlinejson: invalid input")

// Decode supports closed structs, slices and scalar fields, using exact JSON
// tag spelling (or the exported Go field name when no tag exists). It rejects
// unknown/case-aliased/duplicate keys, nulls, trailing values, deep/oversized
// input and invalid UTF-8. Maps, embedded anonymous fields and nested pointers
// are not protocol shapes. The caller's output remains unchanged on failure.
func Decode(data []byte, out any) error {
	if len(data) == 0 || len(data) > MaxBytes || !utf8.Valid(data) {
		return ErrInvalid
	}
	target := reflect.ValueOf(out)
	if !target.IsValid() || target.Kind() != reflect.Ptr || target.IsNil() {
		return ErrInvalid
	}
	shape := target.Elem().Type()
	d := json.NewDecoder(bytes.NewReader(data))
	if err := scan(d, 0, shape); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	value := reflect.New(shape)
	if err := d.Decode(value.Interface()); err != nil {
		return ErrInvalid
	}
	target.Elem().Set(value.Elem())
	return nil
}

func scan(d *json.Decoder, depth int, shape reflect.Type) error {
	if depth > MaxDepth || shape.Kind() == reflect.Ptr || shape.Kind() == reflect.Map || shape.Kind() == reflect.Interface {
		return ErrInvalid
	}
	token, err := d.Token()
	if err != nil {
		return ErrInvalid
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		if token == nil {
			return ErrInvalid
		}
		return nil
	}
	switch delimiter {
	case '{':
		if shape.Kind() != reflect.Struct {
			return ErrInvalid
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < shape.NumField(); i++ {
			field := shape.Field(i)
			if field.PkgPath != "" {
				continue
			}
			if field.Anonymous {
				return ErrInvalid
			}
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			if _, duplicate := fields[name]; duplicate {
				return ErrInvalid
			}
			fields[name] = field.Type
		}
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			name, ok := key.(string)
			field, known := fields[name]
			if err != nil || !ok || !known || seen[name] {
				return ErrInvalid
			}
			seen[name] = true
			if err := scan(d, depth+1, field); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return ErrInvalid
		}
	case '[':
		if shape.Kind() != reflect.Slice {
			return ErrInvalid
		}
		for d.More() {
			if err := scan(d, depth+1, shape.Elem()); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
