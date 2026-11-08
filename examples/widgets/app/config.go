//go:build !js

package app

import (
	"net/http"
	"os"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/scripts"
)

func Configure(c *nextgo.Config) {
	c.BasePath = os.Getenv("NEXTGO_METADATA_BASE_PATH")
	c.AssetPrefix = os.Getenv("NEXTGO_METADATA_ASSET_PREFIX")
	data, err := scripts.NewDataAttributes(map[string]string{"data-site": "example.test", "data-label": `Go "owned" <chart> & café`})
	if err != nil {
		panic(err) // Constant example configuration must remain valid.
	}
	c.EarlyScripts = []scripts.Script{{ID: "widget-settings-v1", Src: "/vendor/settings-fixture-v1.js", Asset: true, Strategy: scripts.BeforeInteractive, Data: data}}
	if c.Phase == nextgo.PhaseStaticExport {
		return // A static host must supply its own response policy.
	}
	c.Middleware = append(c.Middleware, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nonce, err := scripts.NewNonce()
			if err != nil {
				http.Error(w, "Unable to create security policy", http.StatusInternalServerError)
				return
			}
			ctx, err := scripts.WithNonce(r.Context(), nonce)
			if err != nil {
				http.Error(w, "Invalid security policy", http.StatusInternalServerError)
				return
			}
			// uPlot sets inline element styles. Script execution remains nonce
			// based, including native import from the trusted bootstrap.
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'nonce-"+nonce+"' 'strict-dynamic' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
}
