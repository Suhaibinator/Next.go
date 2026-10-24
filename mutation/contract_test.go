package mutation

import (
	"errors"
	"strings"
	"testing"
)

func TestContractIdentityAndValidation(t *testing.T) {
	c, err := New[int, string]("cart/add", "v2")
	if err != nil || c.Name() != "cart/add" || c.Revision() != "v2" || c.Key() != "cart/add/v2" || c.Path() != "/_nextgo/actions/cart/add/v2" {
		t.Fatal(c, err)
	}
	for _, pair := range [][2]string{{"", "v1"}, {"a/../b", "v1"}, {"/name", "v1"}, {"a//b", "v1"}, {"name?query", "v1"}, {"name", ""}, {"name", "../v2"}, {strings.Repeat("a", 257), "v1"}, {"name", strings.Repeat("v", 65)}} {
		if _, err := New[int, int](pair[0], pair[1]); err == nil {
			t.Fatal("accepted invalid contract", pair)
		}
	}
	if err := (Contract[int, int]{}).validate(); err == nil {
		t.Fatal("zero contract accepted")
	}
}

func TestTypedResponseSuccessFailuresAndProtocolRejection(t *testing.T) {
	type output struct {
		Count int `json:"count"`
	}
	result, err := decode[output](200, "1", "v1", "v1", []byte(`{"data":{"count":3},"redirect":"/cart?done=1#items"}`))
	if err != nil || result.Data.Count != 3 || result.Redirect != "/cart?done=1#items" {
		t.Fatal(result, err)
	}
	_, err = decode[output](422, "1", "v1", "v1", []byte(`{"error":{"code":"validation_error","message":"Check amount","fields":{"amount":"Positive only"}}}`))
	var failure *Error
	if !errors.As(err, &failure) || failure.Status != 422 || failure.Code != "validation_error" || failure.Fields["amount"] != "Positive only" {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		status                   int
		protocol, revision, body string
	}{
		{200, "", "v1", `{"data":{"count":1}}`}, {200, "2", "v1", `{"data":{"count":1}}`}, {200, "1", "v2", `{"data":{"count":1}}`},
		{200, "1", "", `{"data":{"count":1}}`}, {201, "1", "v1", `{"data":{"count":1}}`}, {200, "1", "v1", `{}`},
		{200, "1", "v1", `{"data":null}`}, {200, "1", "v1", `{"data":{"count":"wrong"}}`}, {200, "1", "v1", `{"data":{"count":1,"extra":true}}`},
		{200, "1", "v1", `{"data":{"count":1},"extra":true}`}, {200, "1", "v1", `{"data":{"count":1},"data":{"count":2}}`},
		{200, "1", "v1", `{"data":{"count":1,"count":2}}`}, {200, "1", "v1", `{"data":{"count":1}} {}`},
		{200, "1", "v1", `{"error":{"code":"failure","message":"bad"}}`}, {200, "1", "v1", `{"data":{"count":1},"redirect":"//foreign.test/"}`},
		{200, "1", "v1", `{"data":{"count":1},"redirect":"javascript:bad()"}`}, {500, "1", "v1", `{"data":{"count":1}}`},
		{422, "1", "v1", `{"error":{"code":"failure","message":"bad"},"data":null}`}, {422, "1", "v1", `{"error":{"code":"failure","message":"bad"},"redirect":"/"}`},
	} {
		if _, err := decode[output](tc.status, tc.protocol, tc.revision, "v1", []byte(tc.body)); !errors.Is(err, ErrProtocol) {
			t.Fatalf("accepted status %d body %s: %v", tc.status, tc.body, err)
		}
	}
	if value, err := decode[*output](200, "1", "v1", "v1", []byte(`{"data":null}`)); err != nil || value.Data != nil {
		t.Fatal(value, err)
	}
	if value, err := decode[int](200, "1", "v1", "v1", []byte(`{"data":0}`)); err != nil || value.Data != 0 {
		t.Fatal(value, err)
	}
	if err := uniqueJSON([]byte(strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66))); err == nil {
		t.Fatal("excess nesting accepted")
	}
}
