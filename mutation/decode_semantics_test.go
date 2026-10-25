package mutation

import (
	"errors"
	"strings"
	"testing"
)

type customDecodedOutput struct {
	Calls int
	Value string
}

func (value *customDecodedOutput) UnmarshalJSON(data []byte) error {
	value.Calls++
	value.Value = string(data)
	if string(data) == `"reject"` {
		return errors.New("application codec detail")
	}
	return nil
}

func TestDecodePreservesCustomOutputCodecsAndFailurePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, revision, body, reason string
		calls                                  int
	}{
		{"success", "1", "v1", `{"data":"accepted"}`, "", 1},
		{"custom error", "1", "v1", `{"data":"reject"}`, "result does not match output type", 1},
		{"revision before body", "1", "v2", `not-json`, "contract revision changed", 0},
		{"protocol before body", "2", "v1", `not-json`, "missing or unsupported protocol", 0},
		{"unknown envelope field before codec", "1", "v1", `{"data":"accepted","extra":true}`, "invalid response envelope", 0},
		{"duplicate data before codec", "1", "v1", `{"data":"accepted","data":"reject"}`, "invalid JSON envelope", 0},
		{"nested duplicate before codec", "1", "v1", `{"data":{"count":1,"count":2}}`, "invalid JSON envelope", 0},
		{"null before custom struct codec", "1", "v1", `{"data":null}`, "null result for a nonnullable output", 0},
		{"output before redirect", "1", "v1", `{"data":"reject","redirect":"//foreign.test"}`, "result does not match output type", 1},
		{"redirect after output", "1", "v1", `{"data":"accepted","redirect":"//foreign.test"}`, "unsafe redirect metadata", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decode[customDecodedOutput](200, tc.protocol, tc.revision, "v1", []byte(tc.body))
			if got.Data.Calls != tc.calls {
				t.Fatalf("codec calls = %d, want %d", got.Data.Calls, tc.calls)
			}
			if tc.reason == "" {
				if err != nil || got.Data.Value != `"accepted"` {
					t.Fatal(got, err)
				}
				return
			}
			if !errors.Is(err, ErrProtocol) || err.Error() != ErrProtocol.Error()+": "+tc.reason {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(err.Error(), "application codec detail") {
				t.Fatal("application codec error escaped protocol boundary")
			}
			if tc.calls == 1 && got.Data.Value == "" {
				t.Fatal("partial custom output was discarded")
			}
			if got.Redirect != "" {
				t.Fatal("failed redirect metadata escaped", got.Redirect)
			}
		})
	}
}

func TestDecodePreservesPartialTypedOutputOnStrictFailure(t *testing.T) {
	type output struct {
		Count int    `json:"count"`
		Label string `json:"label"`
	}
	result, err := decode[output](200, "1", "v1", "v1", []byte(`{"data":{"count":7,"label":42}}`))
	if !errors.Is(err, ErrProtocol) || result.Data.Count != 7 || result.Redirect != "" {
		t.Fatal(result, err)
	}
}

func TestDecodeNullOutputKinds(t *testing.T) {
	body := []byte(`{"data":null}`)
	if _, err := decode[[]int](200, "1", "v1", "v1", body); err != nil {
		t.Fatal(err)
	}
	if _, err := decode[map[string]int](200, "1", "v1", "v1", body); err != nil {
		t.Fatal(err)
	}
	if _, err := decode[any](200, "1", "v1", "v1", body); err != nil {
		t.Fatal(err)
	}
	if _, err := decode[*int](200, "1", "v1", "v1", body); err != nil {
		t.Fatal(err)
	}
	if _, err := decode[[1]int](200, "1", "v1", "v1", body); !errors.Is(err, ErrProtocol) {
		t.Fatal(err)
	}
	if _, err := decode[bool](200, "1", "v1", "v1", body); !errors.Is(err, ErrProtocol) {
		t.Fatal(err)
	}
	if _, err := decode[string](200, "1", "v1", "v1", body); !errors.Is(err, ErrProtocol) {
		t.Fatal(err)
	}
}
