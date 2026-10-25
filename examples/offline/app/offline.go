//go:build !js

package app

import (
	"os"
	"time"

	"github.com/Suhaibinator/Next.go/offline"
)

// Offline declares saved anonymous public documents, never a current account's
// data. The operator supplies this exact origin; requests cannot select it.
func Offline() offline.Config {
	origin := os.Getenv("NEXTGO_OFFLINE_ORIGIN")
	if origin == "" {
		origin = "http://127.0.0.1:3022"
	}
	return offline.Config{
		ID: "nextgo-offline-demo", Origin: origin,
		BasePath:  os.Getenv("NEXTGO_OFFLINE_BASE_PATH"),
		Semantics: offline.SavedAnonymousPublic,
		Pages: []offline.Page{
			{Path: "/", MaxAge: 24 * time.Hour},
			{Path: "/guide", MaxAge: 24 * time.Hour},
			{Path: "/offline", MaxAge: 24 * time.Hour},
		},
		Fallback: "/offline", Updates: offline.WaitForClosedClients,
	}
}
