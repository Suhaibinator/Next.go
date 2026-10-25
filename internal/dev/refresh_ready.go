package dev

import (
	"encoding/json"
	"io"
	"os"

	"github.com/Suhaibinator/Next.go/internal/ascii"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

const maxDevelopmentReadyBytes = 4 << 10

type developmentReady struct {
	Version    int    `json:"version"`
	Build      string `json:"build"`
	Generation string `json:"generation"`
	Supported  bool   `json:"supported"`
}

// This is a same-native-child startup assertion, never a browser/request claim.
// Missing/invalid/unsupported readiness preserves ordinary rebuild behavior.
func readDevelopmentReady(path, build, generation string) bool {
	if path == "" || !ascii.LowerHex(build, 20) || !ascii.LowerHex(generation, 32) {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxDevelopmentReadyBytes {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxDevelopmentReadyBytes+1))
	if err != nil || len(data) > maxDevelopmentReadyBytes {
		return false
	}
	var ready developmentReady
	var fields map[string]json.RawMessage
	if wirejson.Unique(data) != nil || wirejson.Strict(data, &fields) != nil || len(fields) != 4 {
		return false
	}
	for _, name := range []string{"version", "build", "generation", "supported"} {
		if fields[name] == nil {
			return false
		}
	}
	return wirejson.Strict(data, &ready) == nil && ready.Version == 1 && ready.Build == build && ready.Generation == generation && ready.Supported
}
