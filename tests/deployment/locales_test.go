//go:build deployment

package deployment

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestLocalizedLambdaExecutable(t *testing.T) {
	for _, strategy := range []string{"prefix", "domain"} {
		t.Run(strategy, func(t *testing.T) { lambdaExecutable(t, "/lang", false, strategy) })
	}
}

func TestLocalizedHTTPExecutable(t *testing.T) {
	for _, domain := range []bool{false, true} {
		t.Run(fmt.Sprint(domain), func(t *testing.T) {
			addr := address(t)
			origin := "http://" + addr
			host := addr
			prefix := "/lang/fr"
			domains := ""
			if domain {
				host = "fr.demo.test"
				domains = addr + "=en,fr.demo.test=fr"
				prefix = "/lang"
			}
			p := start(t, []string{"NEXTGO_DEMO_LOCALES=true", "NEXTGO_DEMO_BASE_PATH=/lang", "NEXTGO_DEMO_LOCALE_DOMAINS=" + domains}, "-addr", addr)
			client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			get := func(path string) (*http.Response, error) {
				r, _ := http.NewRequest("GET", origin+path, nil)
				r.Host = host
				return client.Do(r)
			}
			ready := false
			for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
				response, err := get("/lang/api/health")
				if err == nil {
					_ = body(t, response)
					if response.StatusCode == 200 {
						ready = true
						break
					}
				}
				time.Sleep(25 * time.Millisecond)
			}
			if !ready {
				t.Fatal("locale executable not ready", p.logs.String())
			}
			response, err := get(prefix + "/translations")
			if err != nil {
				t.Fatal(err)
			}
			data := body(t, response)
			if response.StatusCode != 200 || response.Header.Get("Content-Language") != "fr" || !bytes.Contains(data, []byte(`<html lang="fr" dir="ltr">`)) || !bytes.Contains(data, []byte("Bonjour")) {
				t.Fatal(response.StatusCode, response.Header, string(data))
			}
			response, err = get(prefix + "/api/locale")
			if err != nil {
				t.Fatal(err)
			}
			if data := body(t, response); response.StatusCode != 200 || !bytes.Contains(data, []byte(`"locale":"fr"`)) {
				t.Fatal(string(data))
			}
			response, err = get("/lang/assets/style.css")
			if err != nil {
				t.Fatal(err)
			}
			_ = body(t, response)
			if response.StatusCode != 200 {
				t.Fatal("shared locale assets failed")
			}
			r, _ := http.NewRequest("GET", origin+"/lang", nil)
			r.Host = host
			r.Header.Set("Accept-Language", "ar")
			response, err = client.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			_ = body(t, response)
			if response.StatusCode != 307 || !strings.Contains(response.Header.Get("Location"), "/lang/ar") {
				t.Fatal("locale negotiation", response.StatusCode, response.Header)
			}
			p.stop(t)
		})
	}
}
