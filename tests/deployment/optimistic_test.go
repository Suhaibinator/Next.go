//go:build deployment

package deployment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var optimisticValue = regexp.MustCompile(`<output[^>]*data-optimistic-value[^>]*>([0-9]+)</output>`)

func checkOptimisticResult(index, status int, payload []byte) error {
	if index == 0 || index == 3 || index == 5 {
		value := 0
		if index == 3 {
			value = 2
		}
		if index == 5 {
			value = 3
		}
		match := optimisticValue.FindSubmatch(payload)
		if status != 200 || len(match) != 2 || string(match[1]) != strconv.Itoa(value) || !bytes.Contains(payload, []byte("Optimistic Go updates")) {
			return fmt.Errorf("optimistic SSR step %d status %d", index, status)
		}
		return nil
	}
	if index == 4 {
		if status != 303 {
			return fmt.Errorf("optimistic progressive redirect status %d", status)
		}
		return nil
	}
	var response struct {
		Data  struct{ Applied, Count int64 }
		Error *struct{ Code string }
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return err
	}
	if index == 1 && (status != 200 || response.Error != nil || response.Data.Applied != 2 || response.Data.Count != 2) {
		return fmt.Errorf("optimistic canonical confirmation status %d: %s", status, payload)
	}
	if index == 2 && (status != 422 || response.Error == nil || response.Error.Code != "validation_error") {
		return fmt.Errorf("optimistic rejection status %d: %s", status, payload)
	}
	return nil
}

func optimisticHTTP(t *testing.T, client *http.Client, origin, base string, slash bool) {
	t.Helper()
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for index := 0; index < 6; index++ {
		path := mountedURL(base, "/optimistic")
		method := "GET"
		data := ""
		if slash {
			path += "/"
		}
		if index == 1 || index == 2 || index == 4 {
			path = mountedURL(base, "/_nextgo/actions/optimistic-increment/v1")
			method = "POST"
			data = `{"amount":5}`
			if index == 2 {
				data = `{"amount":0}`
			}
			if index == 4 {
				data = "amount=1"
			}
		}
		request, err := http.NewRequest(method, origin+path, strings.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if method == "POST" {
			request.Header.Set("Origin", origin)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json")
			if index == 4 {
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				request.Header.Set("Accept", "text/html")
			}
		}
		response, err := noRedirect.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if method == "POST" && (response.Header.Get("X-Nextgo-Action-Protocol") != "1" || response.Header.Get("X-Nextgo-Action-Revision") != "v1") {
			t.Fatal("missing typed action metadata", response.Header)
		}
		if err := checkOptimisticResult(index, response.StatusCode, body(t, response)); err != nil {
			t.Fatal(err)
		}
	}
}
