package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/internal/ascii"
)

func validateFontInventory(entries []assets.FontAsset) error {
	if len(entries) > 256 {
		return fmt.Errorf("managed font inventory exceeds file limit")
	}
	seen := map[string]bool{}
	var total int64
	for _, entry := range entries {
		ext := ""
		switch entry.ContentType {
		case "text/css; charset=utf-8":
			ext = ".css"
		case "font/woff":
			ext = ".woff"
		case "font/woff2":
			ext = ".woff2"
		}
		if ext == "" || !ascii.LowerHex(entry.SHA256, 64) || entry.URL != assets.LocalFontPrefix+entry.SHA256+ext || seen[entry.URL] || entry.Bytes < 1 || entry.Bytes > 32<<20 || entry.Bytes > 64<<20-total {
			return fmt.Errorf("invalid managed font inventory entry")
		}
		seen[entry.URL] = true
		total += entry.Bytes
	}
	return nil
}

// Capture only the bounded native declaration. Never scrape handlers or
// rewrite CSS: every frozen stylesheet already uses relative hashed filenames.
func exportManagedFonts(ctx context.Context, client *http.Client, base, stage string, deployment nextgo.PathConfig, domain, build string, scopes []nextgo.StaticExportScope, entries []assets.FontAsset) error {
	for _, scope := range scopes {
		for _, entry := range entries {
			publicPath := deployment.BasePath + scope.Prefix + entry.URL
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+publicPath, nil)
			if err != nil {
				return err
			}
			request.Host = domain
			request.Header.Set("Accept-Encoding", "identity")
			response, err := client.Do(request)
			if err != nil {
				return err
			}
			data, err := func() ([]byte, error) {
				defer response.Body.Close()
				encoding := response.Header.Get("Content-Encoding")
				if response.StatusCode != http.StatusOK || response.Header.Get("X-Nextgo-Build") != build || response.Header.Get("ETag") != `"`+entry.SHA256+`"` || response.Header.Get("Content-Type") != entry.ContentType || len(response.Header.Values("Set-Cookie")) != 0 || encoding != "" && encoding != "identity" || response.ContentLength >= 0 && response.ContentLength != entry.Bytes {
					return nil, fmt.Errorf("managed font %s has incompatible response identity", publicPath)
				}
				body, err := io.ReadAll(io.LimitReader(response.Body, entry.Bytes+1))
				if err != nil {
					return nil, err
				}
				sum := sha256.Sum256(body)
				if int64(len(body)) != entry.Bytes || hex.EncodeToString(sum[:]) != entry.SHA256 {
					return nil, fmt.Errorf("managed font %s changed content", publicPath)
				}
				return body, nil
			}()
			if err != nil {
				return err
			}
			target := filepath.Join(stage, filepath.FromSlash(strings.TrimPrefix(publicPath, "/")))
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
			if err != nil {
				return fmt.Errorf("managed font publication collision: %w", err)
			}
			_, writeErr := file.Write(data)
			closeErr := file.Close()
			if writeErr != nil {
				return writeErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	return nil
}
