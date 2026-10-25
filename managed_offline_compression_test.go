//go:build !js

package nextgo

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/httpx"
	"github.com/Suhaibinator/Next.go/offline"
	"github.com/Suhaibinator/Next.go/serverless"
)

func managedOfflineLargeWASM(t *testing.T) (*App, string, []byte) {
	t.Helper()
	config, files := managedOfflineFixture(t, "")
	// A valid module header and large inert custom section make the decoded
	// representation exceed both the adapter's body cap and Lambda envelope cap.
	// This fixture is not a compiled Go application or a runtime-execution proof.
	padding := 7 << 20
	body := []byte{'\x00', 'a', 's', 'm', 1, 0, 0, 0, 0}
	var length [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(length[:], uint64(padding+1))
	body = append(body, length[:n]...)
	body = append(body, 0) // Empty custom-section name, followed by opaque bytes.
	body = append(body, make([]byte, padding)...)
	path := "_nextgo/" + config.BuildID + "/app.wasm"
	files[path].Data = body
	for i := range config.OfflineBuild.Files {
		file := &config.OfflineBuild.Files[i]
		if file.Path == path {
			file.SHA256, file.DecodedBytes = offlineDigest(body), int64(len(body))
		}
	}
	app := managedOfflineApp(t, config)
	if err := app.PrepareOfflineBundle(context.Background()); err != nil {
		t.Fatal(err)
	}
	return app, "/" + path, body
}

func managedOfflineGunzip(t *testing.T, compressed []byte) []byte {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestManagedOfflineLargeFrozenAssetCompressionBeforeBuffering(t *testing.T) {
	app, path, body := managedOfflineLargeWASM(t)
	digest := offlineDigest(body)
	response := managedOfflineRequest(app, http.MethodGet, path, http.Header{"Accept-Encoding": {"gzip"}})
	if response.Code != http.StatusOK || response.Header().Get("Content-Encoding") != "gzip" || response.Header().Get("Content-Type") != "application/wasm" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal(response.Code, response.Header())
	}
	if response.Body.Len() >= 4<<20 || response.Header().Get("Content-Length") != "" || response.Header().Get("ETag") != `W/"`+digest+`"` || strings.Join(response.Header().Values("Vary"), ",") != "Accept-Encoding" || !strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
		t.Fatal(response.Body.Len(), response.Header())
	}
	decoded := managedOfflineGunzip(t, response.Body.Bytes())
	if !bytes.Equal(decoded, body) || offlineDigest(decoded) != digest {
		t.Fatal("decoded frozen representation changed")
	}
	identity := managedOfflineRequest(app, http.MethodGet, path, http.Header{"Accept-Encoding": {"identity"}})
	if identity.Code != http.StatusOK || identity.Header().Get("Content-Encoding") != "" || identity.Header().Get("ETag") != `"`+digest+`"` || identity.Header().Get("Content-Length") != strconv.Itoa(len(body)) || !bytes.Equal(identity.Body.Bytes(), body) {
		t.Fatal(identity.Code, identity.Header())
	}
	head := managedOfflineRequest(app, http.MethodHead, path, http.Header{"Accept-Encoding": {"gzip"}})
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Encoding") != "" || head.Header().Get("Content-Length") != strconv.Itoa(len(body)) || head.Header().Get("ETag") != `"`+digest+`"` || strings.Join(head.Header().Values("Vary"), ",") != "Accept-Encoding" {
		t.Fatal(head.Code, head.Header(), head.Body.Len())
	}
	bundle := app.managedOffline.published.Load()
	for _, metadata := range []string{offline.CatalogPath, offline.WorkerScriptPath, managedOfflinePreparedPath(t, bundle, "/public")} {
		got := managedOfflineRequest(app, http.MethodGet, metadata, http.Header{"Accept-Encoding": {"gzip"}})
		if got.Code != http.StatusOK || got.Header().Get("Content-Encoding") != "" || len(got.Header().Values("Vary")) != 0 {
			t.Fatal("asset compression changed a native metadata/document path", metadata, got.Code, got.Header())
		}
	}
}

func TestManagedOfflineLargeAssetBufferedAdapterAndRuntimeCompressionComposition(t *testing.T) {
	app, path, body := managedOfflineLargeWASM(t)
	for _, outerCompression := range []bool{false, true} {
		t.Run(strconv.FormatBool(outerCompression), func(t *testing.T) {
			var handler http.Handler = app
			if outerCompression {
				// Run already installs this outer compression layer. Composition
				// must not double-encode the native frozen-asset response.
				handler = httpx.Compression(handler)
			}
			event := serverless.Request{Version: "2.0", RawPath: path, Headers: map[string]string{"host": "site.test", "accept-encoding": "gzip"}, RequestContext: serverless.RequestContext{DomainName: "site.test", HTTP: serverless.HTTPContext{Method: http.MethodGet, Path: path, Protocol: "HTTP/1.1"}}}
			result, err := serverless.Adapt(context.Background(), handler, event)
			if err != nil || result.StatusCode != http.StatusOK || !result.IsBase64Encoded || result.Headers["content-encoding"] != "gzip" || result.Headers["content-type"] != "application/wasm" || result.Headers["vary"] != "Accept-Encoding" {
				t.Fatal(err, result.StatusCode, result.Headers)
			}
			wire, err := base64.StdEncoding.DecodeString(result.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(managedOfflineGunzip(t, wire), body) {
				t.Fatal("buffered adapter changed decoded asset bytes")
			}
			envelope, err := json.Marshal(result)
			if err != nil || len(envelope) >= 6<<20 {
				t.Fatal("buffered envelope exceeds provider cap", len(envelope), err)
			}
		})
	}
}
