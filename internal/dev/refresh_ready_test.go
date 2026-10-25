package dev

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevelopmentReadyRequiresExactBoundedNativeAssertion(t *testing.T) {
	build, generation := strings.Repeat("a", 20), strings.Repeat("b", 32)
	valid, _ := json.Marshal(developmentReady{Version: 1, Build: build, Generation: generation, Supported: true})
	tests := map[string]string{
		"valid": string(valid), "unsupported": strings.Replace(string(valid), `"supported":true`, `"supported":false`, 1),
		"stale build": strings.Replace(string(valid), build, strings.Repeat("c", 20), 1), "stale generation": strings.Replace(string(valid), generation, strings.Repeat("c", 32), 1),
		"future version": strings.Replace(string(valid), `"version":1`, `"version":2`, 1), "case alias": strings.Replace(string(valid), `"build"`, `"Build"`, 1),
		"duplicate":     strings.TrimSuffix(string(valid), "}") + `,"supported":true}`,
		"unknown field": strings.TrimSuffix(string(valid), "}") + `,"state":{}}`, "trailing": string(valid) + " {}", "array": "[]", "null": "null", "oversized": strings.Repeat(" ", maxDevelopmentReadyBytes+1),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ready")
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if got := readDevelopmentReady(path, build, generation); got != (name == "valid") {
				t.Fatalf("readiness=%t", got)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "missing")
	if readDevelopmentReady(path, build, generation) || readDevelopmentReady(filepath.Dir(path), build, generation) {
		t.Fatal("missing/nonregular readiness accepted")
	}
	if err := os.WriteFile(path, valid, 0600); err != nil {
		t.Fatal(err)
	}
	if readDevelopmentReady(path, "", generation) || readDevelopmentReady(path, build, "") {
		t.Fatal("unbound expected identity accepted")
	}
	link := path + "-link"
	if err := os.Symlink(path, link); err == nil && readDevelopmentReady(link, build, generation) {
		t.Fatal("readiness symlink accepted")
	}
}
