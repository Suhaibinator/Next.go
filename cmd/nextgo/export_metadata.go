package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/internal/ascii"
	"github.com/Suhaibinator/Next.go/metadatafiles"
)

func validateMetadataInventory(deployment nextgo.PathConfig, entries []metadatafiles.Entry) error {
	if err := deployment.Validate(); err != nil {
		return err
	}
	if len(entries) > len(metadatafiles.Names()) {
		return fmt.Errorf("managed metadata inventory exceeds filename limit")
	}
	seen := map[string]bool{}
	var total int64
	for _, entry := range entries {
		if !metadatafiles.IsName(entry.Name) || seen[entry.Name] || entry.Path != deployment.BasePath+"/"+entry.Name || !ascii.LowerHex(entry.Digest, 64) || entry.ETag != `"`+entry.Digest+`"` || entry.Size < 0 || entry.Size > metadatafiles.MaxTotalBytes-total || entry.ContentType == "" || len(entry.ContentType) > 128 || strings.ContainsAny(entry.ContentType, "\r\n\x00") {
			return fmt.Errorf("invalid managed metadata inventory entry")
		}
		seen[entry.Name] = true
		total += entry.Size
	}
	return nil
}

// Fetch only the declared fixed names from the private export server. Do not
// scrape arbitrary handlers or publish an unverified prefix of a response.
func exportManagedMetadata(ctx context.Context, client *http.Client, base, stage string, deployment nextgo.PathConfig, domain, build string, entries []metadatafiles.Entry) error {
	if err := validateMetadataInventory(deployment, entries); err != nil {
		return err
	}
	for _, entry := range entries {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+entry.Path, nil)
		if err != nil {
			return err
		}
		request.Host = domain
		request.Header.Set("Accept-Encoding", "identity")
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		data, readErr := func() ([]byte, error) {
			defer response.Body.Close()
			encoding := response.Header.Get("Content-Encoding")
			if response.StatusCode != http.StatusOK || response.Header.Get("X-Nextgo-Build") != build || response.Header.Get("ETag") != entry.ETag || response.Header.Get("Content-Type") != entry.ContentType || len(response.Header.Values("Set-Cookie")) != 0 || encoding != "" && encoding != "identity" {
				return nil, fmt.Errorf("managed metadata %s has incompatible response identity", entry.Name)
			}
			if response.ContentLength >= 0 && response.ContentLength != entry.Size {
				return nil, fmt.Errorf("managed metadata %s has incompatible response length", entry.Name)
			}
			body, err := io.ReadAll(io.LimitReader(response.Body, entry.Size+1))
			if err != nil {
				return nil, err
			}
			if int64(len(body)) != entry.Size {
				return nil, fmt.Errorf("managed metadata %s changed length", entry.Name)
			}
			actual, err := metadatafiles.Validate(entry.Name, body, metadatafiles.Options{BasePath: deployment.BasePath})
			if err != nil || actual != entry {
				return nil, fmt.Errorf("managed metadata %s changed content or declaration", entry.Name)
			}
			return body, nil
		}()
		if readErr != nil {
			return readErr
		}
		target := filepath.Join(stage, filepath.FromSlash(strings.TrimPrefix(entry.Path, "/")))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("managed metadata publication collision for %s: %w", entry.Name, err)
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
	return nil
}
