//go:build !js

package app_test

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/app"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChoicePreviewContractPreservesRepeatedTopicFieldsAndValidatesMembership(t *testing.T) {
	config := nextgo.Config{}
	app.Configure(&config)
	for _, tc := range []struct {
		body, media string
		status      int
	}{
		{`{"channel":"sms","topics":["wasm","routing"]}`, "application/json", 200},
		{"channel=sms&topics=wasm&topics=routing&intent=preview", "application/x-www-form-urlencoded", 303},
		{"channel=sms&channel=email&topics=ssr", "application/x-www-form-urlencoded", 422},
		{"channel=sms&topics=wasm&topics=wasm", "application/x-www-form-urlencoded", 422},
		{"channel=sms&topics=unknown", "application/x-www-form-urlencoded", 422},
		{"channel=sms&topics=ssr&extra=x", "application/x-www-form-urlencoded", 422},
		{`{"channel":"sms","topics":[]}`, "application/json", 422},
	} {
		request := actionRequest(tc.body, tc.media)
		request.URL.Path = "/choices/preview/v1"
		response := httptest.NewRecorder()
		config.Actions.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatal(tc, response.Code, response.Body.String())
		}
		if tc.status == 200 && !strings.Contains(response.Body.String(), `"topics":["routing","wasm"]`) {
			t.Fatal(response.Body.String())
		}
		if tc.status == 303 && response.Header().Get("Location") != "/choices?applied=1&channel=sms&topics=routing&topics=wasm" {
			t.Fatal(response.Header())
		}
	}
}
