//go:build !js

// A build-time Go hook: no JavaScript runtime or package installer is needed.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Suhaibinator/Next.go/buildassets"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	w, err := buildassets.WorkspaceFromEnv()
	if err == nil {
		err = transform(ctx, w)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func transform(ctx context.Context, w buildassets.Workspace) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var tokens struct{ Accent, Background, Ink string }
	data, err := os.ReadFile(filepath.Join(w.Inputs, "styles", "tokens.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &tokens); err != nil {
		return err
	}
	color := regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	for _, value := range []string{tokens.Accent, tokens.Background, tokens.Ink} {
		if !color.MatchString(value) {
			return fmt.Errorf("design tokens must be six-digit hex colors")
		}
	}
	css, err := os.ReadFile(filepath.Join(w.Inputs, "styles", "main.css"))
	if err != nil {
		return err
	}
	result := strings.NewReplacer("{{accent}}", tokens.Accent, "{{background}}", tokens.Background, "{{ink}}", tokens.Ink).Replace(string(css))
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(w.Outputs, "style.css"), []byte("/* Generated from Go design tokens. */\n"+result), 0644)
}
