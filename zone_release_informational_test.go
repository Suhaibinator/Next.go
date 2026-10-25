//go:build !js

package nextgo

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/zonecontract"
	"github.com/Suhaibinator/Next.go/telemetry"
)

type isolated1xxObservation struct {
	Kind                 string        `json:"kind"`
	Encoding             string        `json:"encoding"`
	Tracing              bool          `json:"tracing"`
	InformationalStatus  int           `json:"informationalStatus"`
	Informational        []int         `json:"informational"`
	InformationalHeaders []http.Header `json:"informationalHeaders"`
	FinalStatus          int           `json:"finalStatus"`
	FinalHeaders         http.Header   `json:"finalHeaders"`
	FinalIdentityValid   *bool         `json:"finalIdentityValid,omitempty"`
	BodySHA256           string        `json:"bodySHA256"`
	ExpectedBodySHA256   string        `json:"expectedBodySHA256"`
	BodyReadError        string        `json:"bodyReadError,omitempty"`
	RecordedStatuses     []int         `json:"recordedStatuses,omitempty"`
}

func isolated1xxEvidence(t *testing.T, name string, observation isolated1xxObservation) {
	t.Helper()
	directory := os.Getenv("NEXTGO_1XX_EVIDENCE_DIR")
	if directory == "" {
		return
	}
	data, err := json.MarshalIndent(observation, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name+".json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("wire evidence: %s", filepath.Join(directory, name+".json"))
}

func TestIsolatedZoneReleaseInformationalWire(t *testing.T) {
	for _, kind := range []string{"ordinary", "release"} {
		for _, encoding := range []string{"identity", "gzip"} {
			for _, tracing := range []bool{false, true} {
				for _, information := range []int{100, 103} {
					name := fmt.Sprintf("%s-%s-trace-%t-info-%d", kind, encoding, tracing, information)
					t.Run(name, func(t *testing.T) {
						server := httptest.NewUnstartedServer(nil)
						t.Cleanup(server.Close)
						declaration, _ := releaseBoundaryConfig(t)
						declaration.BackendHosts = []string{server.Listener.Addr().String()}
						body := strings.Repeat("missing informational-response fixture\n", 32)
						digest := sha256.Sum256([]byte(body))
						observation := isolated1xxObservation{Kind: kind, Encoding: encoding, Tracing: tracing, InformationalStatus: information, ExpectedBodySHA256: hex.EncodeToString(digest[:])}
						var observed sync.Mutex
						if tracing {
							tracer, err := telemetry.New(telemetry.Options{Recorder: func(record telemetry.Record) error {
								observed.Lock()
								defer observed.Unlock()
								observation.RecordedStatuses = append(observation.RecordedStatuses, record.Status)
								return nil
							}})
							if err != nil {
								t.Fatal(err)
							}
							declaration.Zone.Config.Tracing = tracer
						}
						declaration.Zone.Config.Routes = []Route{{Pattern: "/api/info", API: func(w http.ResponseWriter, r *http.Request) {
							w.Header().Set("Link", "</fixture.css>; rel=preload; as=style")
							w.WriteHeader(information)
							w.Header().Del("Link")
							w.Header().Set("Content-Type", "text/plain; charset=utf-8")
							w.Header().Set("Cache-Control", "private, no-store")
							w.Header().Set("X-Isolated-Final", "yes")
							w.WriteHeader(http.StatusNotFound)
							_, _ = io.WriteString(w, body)
						}}}
						if kind == "release" {
							release := releaseBoundaryNew(t, declaration)
							server.Config.Handler = release
						} else {
							config := declaration.Zone.Config
							config.BasePath = declaration.Release.Zone.BasePath
							config.BuildID = declaration.Release.RuntimeID
							app, err := NewContext(context.Background(), config)
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() {
								if err := app.Close(context.Background()); err != nil {
									t.Error(err)
								}
							})
							server.Config.Handler = app
						}
						server.Start()
						transport := &http.Transport{DisableCompression: true, Proxy: nil}
						t.Cleanup(transport.CloseIdleConnections)
						client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
						request, err := http.NewRequest(http.MethodGet, server.URL+"/team/api/info", nil)
						if err != nil {
							t.Fatal(err)
						}
						request.Header.Set("Accept-Encoding", encoding)
						if kind == "release" {
							zonecontract.SetExpectation(request.Header, declaration.Release.Expectation())
						}
						request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{Got1xxResponse: func(code int, headers textproto.MIMEHeader) error {
							observed.Lock()
							defer observed.Unlock()
							observation.Informational = append(observation.Informational, code)
							observation.InformationalHeaders = append(observation.InformationalHeaders, http.Header(headers).Clone())
							return nil
						}}))
						response, err := client.Do(request)
						if err != nil {
							t.Fatal("actual HTTP exchange failed", err)
						}
						defer response.Body.Close()
						reader := io.Reader(response.Body)
						if response.Header.Get("Content-Encoding") == "gzip" {
							compressed, err := gzip.NewReader(response.Body)
							if err != nil {
								t.Fatal(err)
							}
							defer compressed.Close()
							reader = compressed
						}
						data, readError := io.ReadAll(reader)
						observed.Lock()
						observation.FinalStatus, observation.FinalHeaders = response.StatusCode, response.Header.Clone()
						if kind == "release" {
							valid := zonecontract.CheckExpectation(response.Header, declaration.Release.Expectation()) == nil
							observation.FinalIdentityValid = &valid
						}
						actualDigest := sha256.Sum256(data)
						observation.BodySHA256 = hex.EncodeToString(actualDigest[:])
						if readError != nil {
							observation.BodyReadError = readError.Error()
						}
						isolated1xxEvidence(t, name, observation)
						observed.Unlock()
						if response.StatusCode != http.StatusNotFound {
							t.Errorf("final HTTP status=%d, want404 after%d", response.StatusCode, information)
						}
						if readError != nil || string(data) != body {
							t.Errorf("final body changed or truncated: bytes=%d err=%v", len(data), readError)
						}
						if response.Header.Get("X-Isolated-Final") != "yes" || response.Header.Get("Cache-Control") != "private, no-store" || response.Header.Get("Link") != "" {
							t.Errorf("final header updates lost: %v", response.Header)
						}
						if !slices.Equal(observation.Informational, []int{information}) {
							t.Errorf("actual informational statuses=%v, want[%d]", observation.Informational, information)
						}
						if kind == "release" && !*observation.FinalIdentityValid {
							t.Error("final private release identity missing or wrong")
						}
					})
				}
			}
		}
	}
}

func TestIsolatedZoneReleaseSwitchingProtocolsRemainsFinal(t *testing.T) {
	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)
	declaration, _ := releaseBoundaryConfig(t)
	declaration.BackendHosts = []string{server.Listener.Addr().String()}
	declaration.Zone.Config.Routes = []Route{{Pattern: "/api/switch", API: func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
		w.WriteHeader(http.StatusNotFound)
	}}}
	server.Config.Handler = releaseBoundaryNew(t, declaration)
	server.Start()
	transport := &http.Transport{DisableCompression: true, Proxy: nil}
	t.Cleanup(transport.CloseIdleConnections)
	request, err := http.NewRequest(http.MethodGet, server.URL+"/team/api/switch", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept-Encoding", "identity")
	zonecontract.SetExpectation(request.Header, declaration.Release.Expectation())
	response, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	valid := zonecontract.CheckExpectation(response.Header, declaration.Release.Expectation()) == nil
	isolated1xxEvidence(t, "release-101-final", isolated1xxObservation{Kind: "release", Encoding: "identity", InformationalStatus: 101, FinalStatus: response.StatusCode, FinalHeaders: response.Header.Clone(), FinalIdentityValid: &valid})
	if response.StatusCode != http.StatusSwitchingProtocols || !valid {
		t.Fatal("101 lost final status/identity", response.StatusCode, valid)
	}
}
