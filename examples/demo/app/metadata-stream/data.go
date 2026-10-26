//go:build !js

package metadatastream

import (
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/media"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
	"github.com/Suhaibinator/Next.go/seo"
)

const maxSourceBytes = 8 << 10

type settings struct {
	source   string
	timeout  time.Duration
	testMode string
}

// All settings are native operator input, never request/query-selected URLs.
// The optional loopback source gives qualification fixtures a genuine bounded
// HTTP completion gate without placing endpoint details in browser props.
func environmentSettings() (settings, error) {
	s := settings{source: os.Getenv("NEXTGO_DEMO_METADATA_SOURCE"), timeout: 5 * time.Second}
	if text := os.Getenv("NEXTGO_DEMO_METADATA_SOURCE_TIMEOUT"); text != "" {
		value, err := time.ParseDuration(text)
		if err != nil || value < time.Millisecond || value > 30*time.Second {
			return settings{}, errors.New("metadata sample source timeout must be between 1ms and 30s")
		}
		s.timeout = value
	}
	if s.source != "" {
		u, err := url.Parse(s.source)
		if err != nil || len(s.source) > 512 || u.Scheme != "http" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.Opaque != "" || strings.ContainsAny(s.source, "\r\n\t\\") {
			return settings{}, errors.New("metadata sample source requires an explicit loopback HTTP URL")
		}
		address := net.ParseIP(u.Hostname())
		if address == nil || !address.IsLoopback() {
			return settings{}, errors.New("metadata sample source requires a literal loopback address")
		}
		if port := u.Port(); port != "" {
			number, err := strconv.Atoi(port)
			if err != nil || number < 1 || number > 65535 || strconv.Itoa(number) != port {
				return settings{}, errors.New("metadata sample source port is invalid")
			}
		} else if strings.HasSuffix(u.Host, ":") {
			return settings{}, errors.New("metadata sample source port is invalid")
		}
	}
	s.testMode = os.Getenv("NEXTGO_DEMO_METADATA_TEST_MODE")
	if s.testMode != "" {
		if os.Getenv("NEXTGO_DEMO_METADATA_FIXTURE") != "true" || s.source == "" {
			return settings{}, errors.New("metadata sample fixture modes require explicit fixture enablement and a loopback source")
		}
		switch s.testMode {
		case "fail", "invalid", "readonly":
		default:
			return settings{}, errors.New("metadata sample fixture mode is invalid")
		}
	}
	return s, nil
}

type sourceValue struct {
	Version           int    `json:"version"`
	Title             string `json:"title"`
	Description       string `json:"description"`
	Canonical         string `json:"canonical"`
	SocialTitle       string `json:"socialTitle"`
	SocialDescription string `json:"socialDescription"`
}

func (v sourceValue) metadata() seo.Metadata {
	return seo.Metadata{
		Title: v.Title, Description: v.Description, Canonical: v.Canonical, AbsoluteTitle: true,
		OpenGraph: seo.OpenGraph{Title: v.SocialTitle, Description: v.SocialDescription, URL: v.Canonical, Type: "website"},
		Twitter:   seo.Twitter{Card: "summary", Title: v.SocialTitle, Description: v.SocialDescription},
	}
}

func defaultValue() sourceValue {
	return sourceValue{Version: 1,
		Title: "Public metadata resolved — Next.go", Description: "Final metadata arrived without replacing this page's interactive body.",
		Canonical:   media.SiteURL + "/metadata-stream?phase=final",
		SocialTitle: "Resolved public metadata", SocialDescription: "One completed Go metadata contribution.",
	}
}

func loadValue(ctx context.Context, s settings) (sourceValue, error) {
	return nextgo.Memo(ctx, "demo.deferred-metadata.v1:"+s.source, func(loadCtx context.Context) (sourceValue, error) {
		if s.source == "" {
			timer := time.NewTimer(250 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-loadCtx.Done():
				return sourceValue{}, loadCtx.Err()
			case <-timer.C:
				return defaultValue(), nil
			}
		}
		return fetchValue(loadCtx, s)
	})
}

func fetchValue(ctx context.Context, s settings) (sourceValue, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.source, nil)
	if err != nil {
		return sourceValue{}, errors.New("metadata sample source request is invalid")
	}
	// A dedicated transport neither forwards ambient proxy credentials nor
	// reuses an authenticated application's client or request headers.
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: s.timeout}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: s.timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return sourceValue{}, ctx.Err()
		}
		return sourceValue{}, errors.New("metadata sample source request failed")
	}
	defer response.Body.Close()
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || mediaErr != nil || mediaType != "application/json" {
		return sourceValue{}, errors.New("metadata sample source response is invalid")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxSourceBytes+1))
	if ctx.Err() != nil {
		return sourceValue{}, ctx.Err()
	}
	if err != nil || len(data) > maxSourceBytes || !utf8.Valid(data) || wirejson.Unique(data) != nil {
		return sourceValue{}, errors.New("metadata sample source response is invalid")
	}
	var value sourceValue
	if err := wirejson.Strict(data, &value); err != nil || value.Version != 1 || value.Title == "" || len(value.Title) > 256 || value.Description == "" || len(value.Description) > 1024 || value.SocialTitle == "" || len(value.SocialTitle) > 256 || value.SocialDescription == "" || len(value.SocialDescription) > 1024 || len(value.Canonical) > 2048 || seo.AbsoluteURL(value.Canonical) != nil {
		return sourceValue{}, errors.New("metadata sample source data is invalid")
	}
	return value, nil
}
