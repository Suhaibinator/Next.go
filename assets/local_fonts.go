package assets

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"unicode"
)

// LocalFontPrefix is the reserved runtime namespace for frozen local fonts.
// Static exporters must capture these declared files or reject their pages.
const LocalFontPrefix = "/_nextgo/local-fonts/"

// LocalFont selects one explicitly public file and its font-face declaration.
// Face.Source must be empty. Face.Format may be empty or match the file header.
type LocalFont struct {
	File string
	Face Font
}

// LocalFontConfig snapshots licensed public WOFF/WOFF2 files at construction.
// FS must support directory enumeration; symlink path components are rejected.
// The default byte limits are 4 MiB per source file and 32 MiB total source
// bytes. Maximum overrides are 32 MiB per file and 64 MiB total.
type LocalFontConfig struct {
	FS            fs.FS
	Fonts         []LocalFont
	MaxFileBytes  int64
	MaxTotalBytes int64
}

type FontAsset struct {
	URL         string `json:"url"`
	ContentType string `json:"contentType"`
	SHA256      string `json:"sha256"`
	Bytes       int64  `json:"bytes"`
}

type FontManifest struct {
	Version    int         `json:"version"`
	Stylesheet string      `json:"stylesheet"`
	Faces      []Font      `json:"faces"`
	Assets     []FontAsset `json:"assets"`
}

type frozenFontFile struct {
	entry FontAsset
	data  []byte
}

// LocalFonts owns an immutable public snapshot. It retains no source FS and
// executes no font decoder. The browser still validates and decodes the font.
type LocalFonts struct {
	css   string
	faces []Font
	files map[string]frozenFontFile
}

func NewLocalFonts(ctx context.Context, c LocalFontConfig) (*LocalFonts, error) {
	if c.FS == nil || len(c.Fonts) < 1 || len(c.Fonts) > 128 {
		return nil, fmt.Errorf("assets: local fonts require an FS and 1..128 faces")
	}
	if c.MaxFileBytes == 0 {
		c.MaxFileBytes = 4 << 20
	}
	if c.MaxTotalBytes == 0 {
		c.MaxTotalBytes = 32 << 20
	}
	if c.MaxFileBytes < 1 || c.MaxFileBytes > 32<<20 || c.MaxTotalBytes < 1 || c.MaxTotalBytes > 64<<20 {
		return nil, fmt.Errorf("assets: invalid local font byte limits")
	}
	set := &LocalFonts{files: map[string]frozenFontFile{}}
	sources := map[string][]byte{}
	var total int64
	for _, source := range c.Fonts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if source.Face.Source != "" || source.File == "." || len(source.File) > 4096 || strings.Count(source.File, "/") > 31 || !fs.ValidPath(source.File) || strings.ContainsAny(source.File, "\\:") || strings.IndexFunc(source.File, unicode.IsControl) >= 0 {
			return nil, fmt.Errorf("assets: local font requires a relative file and an empty Face.Source")
		}
		data, exists := sources[source.File]
		if !exists {
			var err error
			data, err = readLocalFont(ctx, c.FS, source.File, min(c.MaxFileBytes, c.MaxTotalBytes-total))
			if err != nil {
				return nil, err
			}
			total += int64(len(data))
			sources[source.File] = data
		}
		format, err := localFontFormat(data)
		if err != nil {
			return nil, err
		}
		face := cloneFont(source.Face)
		if face.Format != "" && face.Format != format {
			return nil, fmt.Errorf("assets: declared local font format does not match its header")
		}
		digest := sha256.Sum256(data)
		hash := hex.EncodeToString(digest[:])
		face.Source, face.Format = LocalFontPrefix+hash+"."+format, format
		face, err = normalize(face)
		if err != nil {
			return nil, err
		}
		set.faces = append(set.faces, face)
		set.files[face.Source] = frozenFontFile{FontAsset{face.Source, "font/" + format, hash, int64(len(data))}, data}
	}
	css, err := fontStyleData(set.faces, true)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(css)
	hash := hex.EncodeToString(digest[:])
	set.css = LocalFontPrefix + hash + ".css"
	set.files[set.css] = frozenFontFile{FontAsset{set.css, "text/css; charset=utf-8", hash, int64(len(css))}, css}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return set, nil
}

func cloneFont(f Font) Font {
	for _, field := range []**float64{&f.SizeAdjust, &f.AscentOverride, &f.DescentOverride, &f.LineGapOverride} {
		if *field != nil {
			value := **field
			*field = &value
		}
	}
	return f
}

func (s *LocalFonts) Stylesheet() Resource { return Stylesheet(s.css) }

// Manifest returns owned public metadata; it never includes source file paths.
// Faces[i].Preload() creates a preload for one explicitly chosen critical face.
func (s *LocalFonts) Manifest() FontManifest {
	result := FontManifest{Version: 1, Stylesheet: s.css, Faces: make([]Font, len(s.faces)), Assets: []FontAsset{}}
	for i, face := range s.faces {
		result.Faces[i] = cloneFont(face)
	}
	for _, file := range s.files {
		result.Assets = append(result.Assets, file.entry)
	}
	sort.Slice(result.Assets, func(i, j int) bool { return result.Assets[i].URL < result.Assets[j].URL })
	return result
}

func (s *LocalFonts) Paths() []string {
	result := make([]string, 0, len(s.files))
	for path := range s.files {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

// ServeHTTP serves exact unmounted manifest paths. Config.LocalFonts provides
// managed delivery and static export. Custom servers can register Paths
// themselves; source FS reads never occur.
func (s *LocalFonts) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	file, ok := s.files[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	serveFontBytes(w, r, file.data, file.entry.ContentType, `"`+file.entry.SHA256+`"`)
}

func readLocalFont(ctx context.Context, files fs.FS, name string, limit int64) ([]byte, error) {
	if limit < 1 {
		return nil, fmt.Errorf("assets: local font source byte budget exhausted")
	}
	parts, directory := strings.Split(name, "/"), "."
	for i, part := range parts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := fontSourceEntry(ctx, files, directory, part)
		if err != nil {
			return nil, err
		}
		if info.Mode()&fs.ModeSymlink != 0 || i < len(parts)-1 && !info.IsDir() || i == len(parts)-1 && !info.Mode().IsRegular() {
			return nil, fmt.Errorf("assets: local font source contains a symlink or nonregular component")
		}
		if directory == "." {
			directory = part
		} else {
			directory += "/" + part
		}
	}
	file, err := files.Open(name)
	if err != nil {
		return nil, fmt.Errorf("assets: cannot open local font: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > limit {
		return nil, fmt.Errorf("assets: local font source is nonregular or exceeds its byte budget")
	}
	data, err := io.ReadAll(io.LimitReader(fontContextReader{ctx, file}, info.Size()+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != info.Size() {
		return nil, fmt.Errorf("assets: local font source changed while reading")
	}
	return data, nil
}

func fontSourceEntry(ctx context.Context, files fs.FS, directory, name string) (fs.FileInfo, error) {
	file, err := files.Open(directory)
	if err != nil {
		return nil, fmt.Errorf("assets: cannot enumerate local font source: %w", err)
	}
	defer file.Close()
	reader, ok := file.(fs.ReadDirFile)
	if !ok {
		return nil, fmt.Errorf("assets: local font FS requires bounded directory reads")
	}
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, err := reader.ReadDir(128)
		count += len(entries)
		if count > 1024 {
			return nil, fmt.Errorf("assets: local font directory exceeds 1024 entries")
		}
		for _, entry := range entries {
			if entry.Name() == name {
				return entry.Info()
			}
		}
		if err != nil {
			return nil, fmt.Errorf("assets: local font source not found: %w", err)
		}
		if len(entries) == 0 {
			return nil, fmt.Errorf("assets: local font directory did not make progress")
		}
	}
}

type fontContextReader struct {
	ctx context.Context
	io.Reader
}

func (r fontContextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(data)
}

func localFontFormat(data []byte) (string, error) {
	if len(data) < 44 || binary.BigEndian.Uint32(data[8:12]) != uint32(len(data)) || binary.BigEndian.Uint16(data[14:16]) != 0 {
		return "", fmt.Errorf("assets: invalid WOFF/WOFF2 container header")
	}
	flavor := binary.BigEndian.Uint32(data[4:8])
	tables := binary.BigEndian.Uint16(data[12:14])
	decoded := binary.BigEndian.Uint32(data[16:20])
	if (flavor != 0x00010000 && flavor != 0x4f54544f) || tables < 1 || tables > 256 || decoded < 12+16*uint32(tables) || decoded > 64<<20 {
		return "", fmt.Errorf("assets: invalid or oversized local font declaration")
	}
	switch string(data[:4]) {
	case "wOFF":
		if len(data) < 44+20*int(tables) {
			break
		}
		return "woff", nil
	case "wOF2":
		if len(data) < 48 {
			break
		}
		compressed := binary.BigEndian.Uint32(data[20:24])
		if compressed == 0 || uint64(compressed) > uint64(len(data)-48) {
			break
		}
		return "woff2", nil
	}
	return "", fmt.Errorf("assets: invalid or unsupported local font format")
}
