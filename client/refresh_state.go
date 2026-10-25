package client

import (
	"encoding"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/devrefresh"
)

const refreshStateMaxDepth = 16
const refreshStateMaxNodes = 4096

type refreshValueBudget struct{ bytes, nodes int }

func (b *refreshValueBudget) add(n int) error {
	if n < 0 || n > devrefresh.MaxCellBytes-b.bytes {
		return fmt.Errorf("nextgo: refresh state cell exceeds 16 KiB")
	}
	b.bytes += n
	return nil
}
func (b *refreshValueBudget) quoted(s string) error {
	if err := b.add(2); err != nil {
		return err
	}
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		size := n
		switch {
		case r == utf8.RuneError && n == 1:
			return fmt.Errorf("nextgo: refresh state strings require valid UTF-8")
		case r == '"' || r == '\\' || r == '\b' || r == '\f' || r == '\n' || r == '\r' || r == '\t':
			size = 2
		case r < 0x20 || r == '<' || r == '>' || r == '&' || r == '\u2028' || r == '\u2029':
			size = 6
		}
		if err := b.add(size); err != nil {
			return err
		}
		s = s[n:]
	}
	return nil
}
func refreshPlainType(t reflect.Type) error {
	if t.PkgPath() == "syscall/js" {
		return fmt.Errorf("nextgo: refresh state cannot contain browser values")
	}
	for _, codec := range [...]reflect.Type{reflect.TypeFor[json.Marshaler](), reflect.TypeFor[json.Unmarshaler](), reflect.TypeFor[encoding.TextMarshaler](), reflect.TypeFor[encoding.TextUnmarshaler]()} {
		if t.Implements(codec) || reflect.PointerTo(t).Implements(codec) {
			return fmt.Errorf("nextgo: refresh state cannot use custom codecs")
		}
	}
	switch t.Kind() {
	case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64, reflect.Struct, reflect.Map, reflect.Slice, reflect.Array:
		return nil
	default:
		return fmt.Errorf("nextgo: refresh state requires plain JSON values")
	}
}
func refreshPlainTree(t reflect.Type, depth int, active map[reflect.Type]bool, nodes *int) error {
	if t == nil || depth > refreshStateMaxDepth || *nodes >= refreshStateMaxNodes || active[t] {
		return fmt.Errorf("nextgo: refresh state type depth or recursion limit exceeded")
	}
	*nodes++
	if err := refreshPlainType(t); err != nil {
		return err
	}
	active[t] = true
	defer delete(active, t)
	switch t.Kind() {
	case reflect.Array, reflect.Slice:
		return refreshPlainTree(t.Elem(), depth+1, active, nodes)
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return fmt.Errorf("nextgo: refresh state map requires string keys")
		}
		if err := refreshPlainType(t.Key()); err != nil {
			return err
		}
		return refreshPlainTree(t.Elem(), depth+1, active, nodes)
	case reflect.Struct:
		names := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			tag := strings.Split(field.Tag.Get("json"), ",")
			if field.PkgPath != "" || field.Anonymous || tag[0] == "-" {
				return fmt.Errorf("nextgo: refresh state requires explicit exported struct fields")
			}
			for _, option := range tag[1:] {
				if option != "" && option != "omitempty" {
					return fmt.Errorf("nextgo: refresh state struct tag is unsupported")
				}
			}
			name := field.Name
			if tag[0] != "" {
				name = tag[0]
			}
			if !devrefresh.ValidToken(name) || names[name] {
				return fmt.Errorf("nextgo: refresh state struct requires unique simple JSON names")
			}
			names[name] = true
			if err := refreshPlainTree(field.Type, depth+1, active, nodes); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *refreshValueBudget) value(v reflect.Value, depth int) error {
	if !v.IsValid() || depth > refreshStateMaxDepth || b.nodes >= refreshStateMaxNodes {
		return fmt.Errorf("nextgo: refresh state value depth or node limit exceeded")
	}
	b.nodes++
	if err := refreshPlainType(v.Type()); err != nil {
		return err
	}
	switch v.Kind() {
	case reflect.Bool:
		return b.add(5)
	case reflect.String:
		return b.quoted(v.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return b.add(len(strconv.FormatInt(v.Int(), 10)))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return b.add(len(strconv.FormatUint(v.Uint(), 10)))
	case reflect.Float32, reflect.Float64:
		n := v.Float()
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return fmt.Errorf("nextgo: refresh state requires finite numbers")
		}
		bits := v.Type().Bits()
		format := byte('f')
		absolute := math.Abs(n)
		if absolute != 0 && (bits == 64 && (absolute < 1e-6 || absolute >= 1e21) || bits == 32 && (float32(absolute) < 1e-6 || float32(absolute) >= 1e21)) {
			format = 'e'
		}
		// encoding/json uses these fixed/exponent thresholds; leaving an exponent's
		// optional zero in the bound is conservative by one byte.
		return b.add(len(strconv.FormatFloat(n, format, -1, bits)))
	case reflect.Array, reflect.Slice:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return b.add(4)
		}
		// encoding/json encodes byte slices as base64, without calling application methods.
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			if err := refreshPlainType(v.Type().Elem()); err != nil {
				return err
			}
			if v.Len() > devrefresh.MaxCellBytes {
				return fmt.Errorf("nextgo: refresh state byte slice exceeds cell limit")
			}
			return b.add(2 + 4*((v.Len()+2)/3))
		}
		if v.Len() > refreshStateMaxNodes-b.nodes {
			return fmt.Errorf("nextgo: refresh state node limit exceeded")
		}
		if err := b.add(2 + v.Len()); err != nil {
			return err
		}
		for i := 0; i < v.Len(); i++ {
			if err := b.value(v.Index(i), depth+1); err != nil {
				return err
			}
		}
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("nextgo: refresh state map requires string keys")
		}
		if err := refreshPlainType(v.Type().Key()); err != nil {
			return err
		}
		if v.IsNil() {
			return b.add(4)
		}
		if v.Len() > refreshStateMaxNodes-b.nodes {
			return fmt.Errorf("nextgo: refresh state node limit exceeded")
		}
		if err := b.add(2 + 2*v.Len()); err != nil {
			return err
		}
		iter := v.MapRange()
		for iter.Next() {
			if err := b.quoted(iter.Key().String()); err != nil {
				return err
			}
			if err := b.value(iter.Value(), depth+1); err != nil {
				return err
			}
		}
	case reflect.Struct:
		if err := b.add(2 + 2*v.NumField()); err != nil {
			return err
		}
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			tag := strings.Split(field.Tag.Get("json"), ",")
			if field.PkgPath != "" || field.Anonymous || tag[0] == "-" {
				return fmt.Errorf("nextgo: refresh state requires explicit exported struct fields")
			}
			for _, option := range tag[1:] {
				if option != "" && option != "omitempty" {
					return fmt.Errorf("nextgo: refresh state struct tag is unsupported")
				}
			}
			name := field.Name
			if tag[0] != "" {
				name = tag[0]
			}
			if err := b.quoted(name); err != nil {
				return err
			}
			if err := b.value(v.Field(i), depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// encodeRefreshValue performs bounded reflection before encoding. Neither phase
// invokes custom codecs, copies object graphs, or calls application methods.
func encodeRefreshValue(value any) (json.RawMessage, error) {
	var budget refreshValueBudget
	nodes := 0
	if err := refreshPlainTree(reflect.TypeOf(value), 0, map[reflect.Type]bool{}, &nodes); err != nil {
		return nil, err
	}
	if err := budget.value(reflect.ValueOf(value), 0); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("nextgo: invalid refresh state value")
	}
	if len(raw) > devrefresh.MaxCellBytes {
		return nil, fmt.Errorf("nextgo: refresh state cell exceeds 16 KiB")
	}
	return raw, nil
}
func validateRefreshType[T any]() error {
	nodes := 0
	return refreshPlainTree(reflect.TypeFor[T](), 0, map[reflect.Type]bool{}, &nodes)
}

func decodeRefreshValue[T any](raw json.RawMessage) (T, error) {
	var value T
	if (devrefresh.Cell{Key: "value", Value: raw}).Validate() != nil {
		return value, fmt.Errorf("nextgo: malformed refresh state cell")
	}
	// Validate the destination type without calling user-defined unmarshal code.
	if err := validateRefreshType[T](); err != nil {
		return value, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("nextgo: refresh state type changed")
	}
	if _, err := encodeRefreshValue(value); err != nil {
		return value, err
	}
	return value, nil
}
