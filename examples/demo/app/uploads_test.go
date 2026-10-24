//go:build !js

package app_test

import (
	"bytes"
	"encoding/json"
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/app"
	"github.com/Suhaibinator/Next.go/examples/demo/uploads"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUploadsUseTheSameValidationForJSONAndNativeMultipart(t *testing.T) {
	config := nextgo.Config{}
	app.Configure(&config)
	input := uploads.Input{Title: " Public binary ", Files: []uploads.Content{{Name: "bytes.bin", Data: []byte{0, 1, 255}}, {Name: "empty.txt", Data: []byte{}}}}
	payload, _ := json.Marshal(input)
	request := actionRequest(string(payload), "application/json")
	request.URL.Path = "/files/receive/v1"
	response := httptest.NewRecorder()
	config.Actions.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"size":3`) {
		t.Fatal(response.Code, response.Body.String())
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("title", "Native files")
	_ = writer.WriteField("intent", "receive")
	for _, file := range input.Files {
		part, _ := writer.CreateFormFile("files", file.Name)
		_, _ = part.Write(file.Data)
	}
	_ = writer.Close()
	request = actionRequest(body.String(), writer.FormDataContentType())
	request.URL.Path = "/files/receive/v1"
	response = httptest.NewRecorder()
	config.Actions.ServeHTTP(response, request)
	if response.Code != 303 || !strings.HasPrefix(response.Header().Get("Location"), "/uploads?receipt=") {
		t.Fatal(response.Code, response.Header(), response.Body.String())
	}
	id := strings.TrimPrefix(response.Header().Get("Location"), "/uploads?receipt=")
	receipt, ok, err := uploads.Get(request.Context(), id)
	if err != nil || !ok || len(receipt.Files) != 2 || receipt.Files[0].Size != 3 {
		t.Fatal(receipt, ok, err)
	}
	input.Files[0].Data = make([]byte, uploads.MaxFileBytes+1)
	payload, _ = json.Marshal(input)
	request = actionRequest(string(payload), "application/json")
	request.URL.Path = "/files/receive/v1"
	response = httptest.NewRecorder()
	config.Actions.ServeHTTP(response, request)
	if response.Code != 422 {
		t.Fatal(response.Code, response.Body.String())
	}
	request = actionRequest(body.String(), writer.FormDataContentType())
	request.URL.Path = "/files/receive/v1"
	request.Header.Set("Origin", "https://foreign.test")
	response = httptest.NewRecorder()
	config.Actions.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal(response.Code)
	}
}
