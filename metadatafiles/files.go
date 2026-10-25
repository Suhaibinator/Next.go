//go:build !js

package metadatafiles

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"strings"
)

// ReadStatic reads only the six exact root filenames. It never walks source
// directories or follows named symlinks. Directories are not declarations, so
// existing app/robots.txt/route.go style manual endpoints remain usable.
func ReadStatic(ctx context.Context, source fs.FS) (map[string][]byte, error) {
	files := map[string][]byte{}
	if ctx == nil {
		return nil, fmt.Errorf("metadatafiles: context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source == nil {
		return files, nil
	}
	entries, err := fs.ReadDir(source, ".")
	if err != nil {
		return nil, err
	}
	total := 0
	for _, entry := range entries {
		name := entry.Name()
		canonical := ""
		for _, known := range names {
			if strings.EqualFold(name, known) {
				canonical = known
				break
			}
		}
		if canonical == "" || entry.IsDir() {
			continue
		}
		if name != canonical {
			return nil, fmt.Errorf("metadatafiles: metadata filename must use exact case %s", canonical)
		}
		if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil, fmt.Errorf("metadatafiles: %s must be a regular file", name)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := source.Open(name)
		if err != nil {
			return nil, err
		}
		info, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			return nil, statErr
		}
		limit := fileLimit(name)
		if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > int64(limit) {
			file.Close()
			return nil, fmt.Errorf("metadatafiles: %s exceeds its size/type limit", name)
		}
		data, readErr := io.ReadAll(io.LimitReader(contextReader{ctx, file}, int64(limit)+1))
		closeErr := file.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(data) > limit || int64(len(data)) != info.Size() {
			return nil, fmt.Errorf("metadatafiles: %s changed size or exceeds limit", name)
		}
		if len(data) > MaxTotalBytes-total {
			return nil, fmt.Errorf("metadatafiles: metadata files exceed total byte limit")
		}
		total += len(data)
		files[name] = data
	}
	return files, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
func fileLimit(name string) int {
	switch name {
	case "robots.txt":
		return MaxRobotsBytes
	case "sitemap.xml":
		return MaxSitemapBytes
	case "manifest.webmanifest":
		return MaxManifestBytes
	default:
		return MaxImageBytes
	}
}
