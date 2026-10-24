//go:build !js

package app_test

import (
	"encoding/json"
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/app"
	"github.com/Suhaibinator/Next.go/examples/demo/profiles"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProfileContractNativeFormsValidationAndFreshSSRData(t *testing.T) {
	config := nextgo.Config{}
	app.Configure(&config)
	id := "native-app-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	payload := profiles.Profile{ID: id, Version: "0", Name: " Native form ", Email: "native@example.test", Role: "reader"}
	data, _ := json.Marshal(payload)
	request := actionRequest(string(data), "application/json")
	request.URL.Path = "/profile/save/v1"
	response := httptest.NewRecorder()
	config.Actions.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"version":"1"`) {
		t.Fatal(response.Code, response.Body.String())
	}
	request = actionRequest("id="+id+"-post&version=0&name=Progressive&email=native%40example.test&role=editor&newsletter=on&intent=save", "application/x-www-form-urlencoded")
	request.URL.Path = "/profile/save/v1"
	response = httptest.NewRecorder()
	config.Actions.ServeHTTP(response, request)
	if response.Code != 303 || response.Header().Get("Location") != "/forms?id="+id+"-post&saved=1" {
		t.Fatal(response.Code, response.Header(), response.Body.String())
	}
	for _, body := range []string{"id=bad&version=0&name=One&name=Two&email=a%40b.test&role=reader", "id=bad&version=0&name=One&email=a%40b.test&role=reader&extra=x"} {
		request = actionRequest(body, "application/x-www-form-urlencoded")
		request.URL.Path = "/profile/save/v1"
		response = httptest.NewRecorder()
		config.Actions.ServeHTTP(response, request)
		if response.Code != 422 {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	request = actionRequest(string(data), "application/json")
	request.Header.Set("Origin", "https://foreign.test")
	request.URL.Path = "/profile/save/v1"
	response = httptest.NewRecorder()
	config.Actions.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal(response.Code)
	}
}
