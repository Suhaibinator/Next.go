//go:build !js

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Suhaibinator/Next.go/buildassets"
)

func styleWorkspace(t *testing.T, tokens string) buildassets.Workspace {
	t.Helper()
	root := t.TempDir()
	w := buildassets.Workspace{Project: root, Inputs: filepath.Join(root, "inputs"), Outputs: filepath.Join(root, "outputs")}
	for _, directory := range []string{filepath.Join(w.Inputs, "styles"), w.Outputs, filepath.Join(w.Dependencies(), "tokens")} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range map[string]string{
		filepath.Join(w.Inputs, "styles", "main.css"):            "body{color:{{accent}};background:{{background}};border-color:{{ink}}}",
		filepath.Join(w.Dependencies(), "tokens", "tokens.json"): tokens,
		// This deliberately invalid source file must never be read by the renderer.
		filepath.Join(w.Inputs, "styles", "tokens.json"): "invalid original source",
	} {
		if err := os.WriteFile(name, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

func TestCSSRendererUsesDeclaredDependency(t *testing.T) {
	w := styleWorkspace(t, `{"accent":"#c7512b","background":"#fbf6ed","ink":"#292621"}`)
	if err := transform(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(w.Outputs, "style.css"))
	if err != nil || string(data) != "/* Generated from Go design tokens. */\nbody{color:#c7512b;background:#fbf6ed;border-color:#292621}" {
		t.Fatal("renderer did not use normalized dependency", string(data), err)
	}
	if _, err := os.Stat(filepath.Join(w.Outputs, "tokens.json")); !os.IsNotExist(err) {
		t.Fatal("renderer published the intermediate", err)
	}
}

func TestCSSRendererRejectsInvalidDependencyBeforeOutput(t *testing.T) {
	w := styleWorkspace(t, `{"accent":"red","background":"#fbf6ed","ink":"#292621"}`)
	if err := transform(context.Background(), w); err == nil {
		t.Fatal("accepted invalid normalized dependency")
	}
	if _, err := os.Stat(filepath.Join(w.Outputs, "style.css")); !os.IsNotExist(err) {
		t.Fatal("invalid dependency published CSS", err)
	}
}
