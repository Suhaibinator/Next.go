//go:build deployment

package deployment

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const formSteps = 18

func formStep(index int) (method, path, query, payload, media string) {
	method, path, query = "GET", "/forms", "id=deploy-profile"
	if index == 1 || index == 2 {
		method, path, query, media = "POST", "/_nextgo/actions/profile/save/v1", "", "application/json"
		payload = `{"id":"deploy-profile","version":"0","name":" Deployment ","email":"deploy@example.test","role":"editor","newsletter":true}`
	}
	if index == 3 {
		method, path, query, media = "POST", "/_nextgo/actions/profile/save/v1", "", "application/x-www-form-urlencoded"
		payload = "id=deploy-profile&version=1&name=Progressive&email=native%40example.test&role=reader&newsletter=on"
	}
	if index == 5 {
		path, query = "/field-preview", ""
	}

	switch index {
	case 6:
		path, query = "/choices", ""
	case 7:
		method, path, query, payload, media = "POST", "/_nextgo/actions/choices/preview/v1", "", `{"channel":"sms","topics":["wasm","routing"]}`, "application/json"
	case 8:
		method, path, query, payload, media = "POST", "/_nextgo/actions/choices/preview/v1", "", "channel=sms&topics=wasm&topics=routing&intent=preview", "application/x-www-form-urlencoded"
	case 9:
		path, query = "/choices", "applied=1&channel=sms&topics=routing&topics=wasm"
	case 10:
		path, query = "/choice-preview", ""
	case 11:
		path, query = "/uploads", ""
	case 12:
		method, path, query, payload, media = "POST", "/_nextgo/actions/files/receive/v1", "", `{"title":"Deployment files","files":[{"name":"binary.bin","data":"AAH/"}]}`, "application/json"
	case 13, 17:
		method, path, query = "POST", "/_nextgo/actions/files/receive/v1", ""
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("title", "Native deployment files")
		part, _ := writer.CreateFormFile("files", "native.bin")
		_, _ = part.Write([]byte{0, 1, 255})
		_ = writer.Close()
		payload, media = body.String(), writer.FormDataContentType()
	case 14:
		path, query = "/uploads", "" // Each harness supplies the returned native receipt URL.
	case 15:
		method, path, query, payload, media = "POST", "/_nextgo/actions/files/receive/v1", "", `{"title":"Oversized","files":[{"name":"big.bin","data":"`+strings.Repeat("A", 349528)+`"}]}`, "application/json"
	case 16:
		path, query = "/file-preview", ""

	}
	return
}
func checkFormStep(index, status int, body []byte, location string) error {
	switch index {
	case 0, 4, 5:
		if status != 200 || !bytes.Contains(body, []byte(`data-nextgo-island="profileform"`)) {
			return fmt.Errorf("form SSR step %d status %d", index, status)
		}
		if index == 4 && !bytes.Contains(body, []byte(`value="Progressive"`)) {
			return fmt.Errorf("updated form SSR absent")
		}
		if index == 5 && bytes.Contains(body, []byte(`/_nextgo/actions/`)) {
			return fmt.Errorf("local preview depends on a server action")
		}
	case 6, 9, 10:
		if status != 200 || !bytes.Contains(body, []byte(`data-nextgo-island="choiceform"`)) {
			return fmt.Errorf("choice SSR step %d status %d", index, status)
		}
		if index == 9 && !bytes.Contains(body, []byte(`sms: routing, wasm`)) {
			return fmt.Errorf("native selected choices missing from fresh SSR")
		}
		if index == 10 && bytes.Contains(body, []byte(`/_nextgo/actions/`)) {
			return fmt.Errorf("local choices require a server action")
		}
	case 7:
		var result struct {
			Data struct {
				Channel string
				Topics  []string
			}
		}
		if err := json.Unmarshal(body, &result); err != nil || status != 200 || result.Data.Channel != "sms" || len(result.Data.Topics) != 2 || result.Data.Topics[0] != "routing" || result.Data.Topics[1] != "wasm" {
			return fmt.Errorf("choice canonical response %d: %s", status, body)
		}
	case 8:
		if status != 303 || location != "/choices?applied=1&channel=sms&topics=routing&topics=wasm" {
			return fmt.Errorf("native repeated choices lost: %d %q", status, location)
		}
	case 11, 14, 16:
		if status != 200 || !bytes.Contains(body, []byte(`data-nextgo-island="fileform"`)) {
			return fmt.Errorf("upload SSR step %d status %d", index, status)
		}
		if index == 14 && !bytes.Contains(body, []byte(`native.bin (3 bytes):`)) {
			return fmt.Errorf("native uploaded metadata absent from fresh SSR")
		}
		if index == 16 && bytes.Contains(body, []byte(`/_nextgo/actions/`)) {
			return fmt.Errorf("local file preview requires server action")
		}
	case 12, 17:
		var result struct {
			Data struct {
				ID    string
				Files []struct {
					Name   string
					Size   int
					SHA256 string
				}
			}
		}
		if err := json.Unmarshal(body, &result); err != nil || status != 200 || len(result.Data.ID) != 32 || len(result.Data.Files) != 1 || result.Data.Files[0].Size != 3 || len(result.Data.Files[0].SHA256) != 64 {
			return fmt.Errorf("typed upload response %d: %s", status, body)
		}
	case 13:
		if status != 303 || !strings.HasPrefix(location, "/uploads?receipt=") || len(strings.TrimPrefix(location, "/uploads?receipt=")) != 32 {
			return fmt.Errorf("native upload response %d %q", status, location)
		}
	case 15:
		if status != 422 || !bytes.Contains(body, []byte(`"files"`)) {
			return fmt.Errorf("oversized upload accepted: %d", status)
		}

	case 1:
		var result struct {
			Data struct{ Name, Version string }
		}
		if err := json.Unmarshal(body, &result); err != nil || status != 200 || result.Data.Name != "Deployment" || result.Data.Version != "1" {
			return fmt.Errorf("form canonical response %d: %s", status, body)
		}
	case 2:
		if status != 422 || !bytes.Contains(body, []byte(`"version"`)) {
			return fmt.Errorf("stale form version accepted %d", status)
		}
	case 3:
		if status != 303 || location != "/forms?id=deploy-profile&saved=1" {
			return fmt.Errorf("form progressive redirect %d %q", status, location)
		}
	}
	return nil
}
func TestFormsHTTPExecutable(t *testing.T) {
	addr := address(t)
	process := start(t, nil, "-addr", addr)
	defer process.stop(t)
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	origin := "http://" + addr
	var receiptLocation string
	for index := 0; index < formSteps; index++ {
		method, path, query, payload, media := formStep(index)
		if index == 14 {
			target, err := url.Parse(receiptLocation)
			if err != nil || target.Path != "/uploads" {
				t.Fatal("missing native receipt", receiptLocation)
			}
			path, query = target.Path, target.RawQuery
		}

		if query != "" {
			path += "?" + query
		}
		request, _ := http.NewRequest(method, origin+path, strings.NewReader(payload))
		if method == "POST" {
			request.Header.Set("Origin", origin)
			request.Header.Set("Content-Type", media)
			request.Header.Set("Accept", "application/json")
			if index == 3 || index == 8 || index == 13 {
				request.Header.Set("Accept", "text/html")
			}
		}
		var response *http.Response
		for until := time.Now().Add(5 * time.Second); ; {
			var err error
			response, err = client.Do(request)
			if err == nil {
				break
			}
			if time.Now().After(until) {
				t.Fatal(err, process.logs.String())
			}
			time.Sleep(25 * time.Millisecond)
		}
		if index == 13 {
			receiptLocation = response.Header.Get("Location")
		}
		if err := checkFormStep(index, response.StatusCode, body(t, response), response.Header.Get("Location")); err != nil {
			t.Fatal(err)
		}
	}
}
func TestFormsLambdaExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			var issued, reports atomic.Int32
			var receiptLocation atomic.Value
			receiptLocation.Store("")
			done := make(chan struct{})
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/next") {
					index := int(issued.Add(1)) - 1
					if index >= formSteps {
						if index == formSteps {
							close(done)
						}
						<-r.Context().Done()
						return
					}
					method, path, query, payload, media := formStep(index)
					if index == 14 {
						target, err := url.Parse(receiptLocation.Load().(string))
						if err != nil || target.Path != "/uploads" {
							t.Error("missing native receipt")
						}
						path, query = target.Path, target.RawQuery
					}
					base64Body := false
					if strings.HasPrefix(media, "multipart/") {
						payload = base64.StdEncoding.EncodeToString([]byte(payload))
						base64Body = true
					}

					headers := map[string]string{"host": "demo.test", "origin": "https://demo.test"}
					if method == "POST" {
						headers["content-type"] = media
						headers["accept"] = "application/json"
						if index == 3 || index == 8 || index == 13 {
							headers["accept"] = "text/html"
						}
					}
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(index))
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
					_ = json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": path, "rawQueryString": query, "headers": headers, "body": payload, "isBase64Encoded": base64Body, "requestContext": map[string]any{"domainName": "demo.test", "http": map[string]string{"method": method, "path": path}}})
					return
				}
				data, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
				if err != nil {
					t.Error(err)
				}
				var envelope httpEnvelope
				var payload []byte
				if mode == "buffered" {
					if err := json.Unmarshal(data, &envelope); err != nil {
						t.Error(err)
					}
					payload = []byte(envelope.Body)
					if envelope.Base64 {
						payload, err = base64.StdEncoding.DecodeString(envelope.Body)
						if err != nil {
							t.Error(err)
						}
					}
				} else {
					split := bytes.Index(data, make([]byte, 8))
					if split < 0 {
						t.Error("missing stream delimiter")
						w.WriteHeader(500)
						return
					}
					if err := json.Unmarshal(data[:split], &envelope); err != nil {
						t.Error(err)
					}
					payload = data[split+8:]
				}
				index := int(reports.Add(1)) - 1
				location := ""
				for name, value := range envelope.Headers {
					if strings.EqualFold(name, "Location") {
						location = value
					}
				}
				if index == 13 {
					receiptLocation.Store(location)
				}
				if err := checkFormStep(index, envelope.StatusCode, payload, location); err != nil {
					t.Error(err)
				}
				w.WriteHeader(202)
			}))
			defer runtime.Close()
			process := start(t, []string{"AWS_LAMBDA_RUNTIME_API=" + strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE=" + mode})
			select {
			case <-done:
			case <-process.done:
				t.Fatal(process.logs.String())
			case <-time.After(10 * time.Second):
				t.Fatal("form runtime did not finish", process.logs.String())
			}
			process.stop(t)
			if reports.Load() != formSteps {
				t.Fatal("missing form reports", reports.Load())
			}
		})
	}
}
