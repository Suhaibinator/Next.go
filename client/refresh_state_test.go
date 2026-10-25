package client

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/devrefresh"
)

type refreshCustomMarshal int

var refreshCodecCalls int

func (refreshCustomMarshal) MarshalJSON() ([]byte, error) {
	refreshCodecCalls++
	return []byte(`1`), nil
}

type refreshCustomUnmarshal int

func (*refreshCustomUnmarshal) UnmarshalJSON([]byte) error { refreshCodecCalls++; return nil }

type refreshCustomText string

func (refreshCustomText) MarshalText() ([]byte, error) {
	refreshCodecCalls++
	return []byte("unsafe"), nil
}

type refreshRecursiveMap map[string]refreshRecursiveMap
type refreshRecursiveSlice []refreshRecursiveSlice

func TestRefreshStatePlainJSONCopy(t *testing.T) {
	type value struct {
		Name    string             `json:"name"`
		Count   int                `json:"count"`
		Flags   []bool             `json:"flags"`
		Weights map[string]float64 `json:"weights"`
		Bytes   []byte             `json:"bytes"`
	}
	initial := value{"label<&\n", 42, []bool{true, false}, map[string]float64{"ratio": 1e20}, []byte{1, 2, 3}}
	raw, err := encodeRefreshValue(initial)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeRefreshValue[value](raw)
	if err != nil || !reflect.DeepEqual(initial, restored) {
		t.Fatal(restored, err)
	}
	restored.Flags[0] = false
	restored.Weights["ratio"] = 2
	restored.Bytes[0] = 8
	if !initial.Flags[0] || initial.Weights["ratio"] != 1e20 || initial.Bytes[0] != 1 {
		t.Fatal("state graph was retained")
	}
	if _, err := decodeRefreshValue[value]([]byte(`{"name":"x","extra":true}`)); err == nil {
		t.Fatal("accepted changed field shape")
	}
	if _, err := decodeRefreshValue[int]([]byte(`"string"`)); err == nil {
		t.Fatal("accepted changed type")
	}
}
func TestRefreshStateRejectsUnsafeValuesBeforeCodec(t *testing.T) {
	recursiveMap := refreshRecursiveMap{}
	recursiveMap["self"] = recursiveMap
	recursiveSlice := make(refreshRecursiveSlice, 1)
	recursiveSlice[0] = recursiveSlice
	duplicateTags := reflect.New(reflect.StructOf([]reflect.StructField{{Name: "A", Type: reflect.TypeFor[int](), Tag: `json:"x"`}, {Name: "B", Type: reflect.TypeFor[int](), Tag: `json:"x"`}})).Elem().Interface()
	for name, value := range map[string]any{
		"pointer": new(int), "interface-field": struct{ Value any }{1}, "function": func() {}, "channel": make(chan int), "complex": complex(1, 2), "nan": math.NaN(), "infinity": math.Inf(1),
		"marshal": refreshCustomMarshal(1), "unmarshal": refreshCustomUnmarshal(1), "text": refreshCustomText("x"), "hidden-codec": map[string]refreshCustomUnmarshal{}, "nil-codec-slice": []refreshCustomMarshal(nil),
		"duplicate-tags": duplicateTags, "invalid-utf8": "\xff", "invalid-tag": struct {
			Value int `json:"\\"`
		}{1},
		"private": struct{ private int }{1}, "ignored": struct {
			Value int `json:"-"`
		}{1}, "string-tag": struct {
			Value int `json:",string"`
		}{1}, "iszero-tag": struct {
			Value int `json:",omitzero"`
		}{1},
		"map-cycle": recursiveMap, "slice-cycle": recursiveSlice, "key-type": map[int]string{1: "x"},
	} {
		t.Run(name, func(t *testing.T) {
			refreshCodecCalls = 0
			if _, err := encodeRefreshValue(value); err == nil {
				t.Fatal("accepted unsafe state")
			}
			if refreshCodecCalls != 0 {
				t.Fatal("invoked application codec")
			}
		})
	}
	refreshCodecCalls = 0
	if _, err := decodeRefreshValue[map[string]refreshCustomUnmarshal]([]byte(`{"x":1}`)); err == nil || refreshCodecCalls != 0 {
		t.Fatal("destination codec invoked", err, refreshCodecCalls)
	}
}
func TestRefreshStatePreflightLimitsMatchJSONNumberAndStringEncoding(t *testing.T) {
	for _, value := range []any{1e20, 1e-6, 1e-7, math.MaxFloat64, float32(1e20), float32(1e-6), "<>&\"\n\u2028\u2029"} {
		var budget refreshValueBudget
		if err := budget.value(reflect.ValueOf(value), 0); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if budget.bytes < len(raw) {
			t.Fatal("preflight undercount", value, budget.bytes, len(raw))
		}
	}
	floats := make([]float64, 1000)
	for i := range floats {
		floats[i] = 1e20
	}
	var budget refreshValueBudget
	if err := budget.value(reflect.ValueOf(floats), 0); err == nil {
		t.Fatal("float fixed notation bypassed preflight cell limit")
	}
	for _, value := range []any{strings.Repeat("x", devrefresh.MaxCellBytes), make([]int, refreshStateMaxNodes+1), make([]byte, devrefresh.MaxCellBytes)} {
		if _, err := encodeRefreshValue(value); err == nil {
			t.Fatal("accepted oversized state")
		}
	}
	if _, err := encodeRefreshValue(strings.Repeat("x", devrefresh.MaxCellBytes-2)); err != nil {
		t.Fatal("bounded string rejected", err)
	}
}
