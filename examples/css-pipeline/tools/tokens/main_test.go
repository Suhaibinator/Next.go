//go:build !js

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/buildassets"
)

func tokenWorkspace(t *testing.T, data string) buildassets.Workspace {
	t.Helper()
	root := t.TempDir()
	w := buildassets.Workspace{Project: root, Inputs: filepath.Join(root, "inputs"), Outputs: filepath.Join(root, "outputs")}
	if err := os.MkdirAll(filepath.Join(w.Inputs, "styles"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(w.Outputs, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Inputs, "styles", "tokens.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestCSSTokenNormalizerCanonicalizesWithoutChangingInput(t *testing.T) {
	original := `{"ink":"#292621","accent":" #C7512B ","background":"#FBF6ED"}`
	w := tokenWorkspace(t, original)
	if err := normalize(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(w.Outputs, "tokens.json"))
	if err != nil || string(data) != "{\"accent\":\"#c7512b\",\"background\":\"#fbf6ed\",\"ink\":\"#292621\"}\n" {
		t.Fatal("unexpected normalized tokens", string(data), err)
	}
	input, err := os.ReadFile(filepath.Join(w.Inputs, "styles", "tokens.json"))
	if err != nil || string(input) != original {
		t.Fatal("normalizer modified its input", err)
	}
}

func TestCSSTokenNormalizerRejectsInvalidInputBeforeOutput(t *testing.T) {
	for name, data := range map[string]string{
		"missing":           `{"accent":"#c7512b","background":"#fbf6ed"}`,
		"duplicate":         `{"accent":"#c7512b","accent":"#000000","background":"#fbf6ed","ink":"#292621"}`,
		"unknown":           `{"accent":"#c7512b","background":"#fbf6ed","ink":"#292621","other":"#000000"}`,
		"invalid-color":     `{"accent":"red","background":"#fbf6ed","ink":"#292621"}`,
		"non-string":        `{"accent":17,"background":"#fbf6ed","ink":"#292621"}`,
		"trailing-document": `{"accent":"#c7512b","background":"#fbf6ed","ink":"#292621"} {}`,
		"oversized":         strings.Repeat(" ", maxTokenBytes+1),
		"not-object":        `null`,
	} {
		t.Run(name, func(t *testing.T) {
			w := tokenWorkspace(t, data)
			if err := normalize(context.Background(), w); err == nil {
				t.Fatal("accepted invalid design tokens")
			}
			if _, err := os.Stat(filepath.Join(w.Outputs, "tokens.json")); !os.IsNotExist(err) {
				t.Fatal("invalid input produced an intermediate", err)
			}
		})
	}
	w := tokenWorkspace(t, `{"accent":"#c7512b","background":"#fbf6ed","ink":"#292621"}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := normalize(ctx, w); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
	if _, err := os.Stat(filepath.Join(w.Outputs, "tokens.json")); !os.IsNotExist(err) {
		t.Fatal("canceled transform produced an intermediate", err)
	}
}
