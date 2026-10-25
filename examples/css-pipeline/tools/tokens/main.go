//go:build !js

// A build-time Go adapter that validates and normalizes private color tokens.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/Suhaibinator/Next.go/buildassets"
)

const maxTokenBytes = 4096

type palette struct {
	Accent     string `json:"accent"`
	Background string `json:"background"`
	Ink        string `json:"ink"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	w, err := buildassets.WorkspaceFromEnv()
	if err == nil {
		err = normalize(ctx, w)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func normalize(ctx context.Context, w buildassets.Workspace) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	input, err := os.Open(filepath.Join(w.Inputs, "styles", "tokens.json"))
	if err != nil {
		return err
	}
	defer input.Close()
	data, err := io.ReadAll(io.LimitReader(input, maxTokenBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxTokenBytes {
		return fmt.Errorf("design token input exceeds 4 KiB")
	}
	tokens, err := decodePalette(data)
	if err != nil {
		return err
	}
	canonical, err := json.Marshal(tokens)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(w.Outputs, "tokens.json"), append(canonical, '\n'), 0644)
}

func decodePalette(data []byte) (palette, error) {
	var tokens palette
	invalid := func() (palette, error) {
		return palette{}, fmt.Errorf("design tokens require exactly accent, background and ink as six-digit hex colors")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return invalid()
	}
	seen := make(map[string]bool, 3)
	for decoder.More() {
		raw, err := decoder.Token()
		if err != nil {
			return invalid()
		}
		key, ok := raw.(string)
		if !ok || seen[key] {
			return invalid()
		}
		seen[key] = true
		var value string
		if err := decoder.Decode(&value); err != nil {
			return invalid()
		}
		value = strings.ToLower(strings.TrimSpace(value))
		if !hexColor(value) {
			return invalid()
		}
		switch key {
		case "accent":
			tokens.Accent = value
		case "background":
			tokens.Background = value
		case "ink":
			tokens.Ink = value
		default:
			return invalid()
		}
	}
	last, err := decoder.Token()
	if err != nil || last != json.Delim('}') || len(seen) != 3 {
		return invalid()
	}
	if _, err := decoder.Token(); err != io.EOF {
		return invalid()
	}
	return tokens, nil
}

func hexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, c := range value[1:] {
		if c < '0' || c > '9' {
			if c < 'a' || c > 'f' {
				return false
			}
		}
	}
	return true
}
