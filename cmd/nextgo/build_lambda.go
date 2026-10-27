package main

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/project"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

const (
	lambdaMaxZIP       = 50000000
	lambdaMaxBootstrap = 250000000
	lambdaMaxManifest  = 1 << 20
	lambdaMaxAssets    = 4096
	lambdaBufferedBody = 4 << 20
)

type lambdaELF struct {
	Machine   string `json:"machine"`
	Class     string `json:"class"`
	Type      string `json:"type"`
	Entry     uint64 `json:"entry"`
	Static    bool   `json:"static"`
	GoVersion string `json:"goVersion"`
}
type lambdaFile struct {
	Path                         string `json:"path"`
	SHA256                       string `json:"sha256"`
	Bytes                        int64  `json:"bytes"`
	IdentityExceedsBufferedLimit bool   `json:"identityExceedsBufferedLimit,omitempty"`
}
type lambdaAsset struct {
	EmbeddedPath                 string `json:"embeddedPath"`
	DefaultRequestPath           string `json:"defaultRequestPath,omitempty"`
	SHA256                       string `json:"sha256"`
	Bytes                        int64  `json:"bytes"`
	IdentityExceedsBufferedLimit bool   `json:"identityExceedsBufferedLimit,omitempty"`
}

type lambdaManifest struct {
	Format                string        `json:"format"`
	Runtime               string        `json:"runtime"`
	Handler               string        `json:"handler"`
	Architecture          string        `json:"architecture"`
	GOOS                  string        `json:"goos"`
	GOARCH                string        `json:"goarch"`
	CGOEnabled            string        `json:"cgoEnabled"`
	ISABaseline           string        `json:"isaBaseline"`
	RuntimeID             string        `json:"runtimeId"`
	SourceFingerprint     string        `json:"sourceFingerprint"`
	Bootstrap             lambdaFile    `json:"bootstrap"`
	Archive               lambdaFile    `json:"archive"`
	ELF                   lambdaELF     `json:"elf"`
	Assets                []lambdaAsset `json:"assets"`
	BufferedTransport     string        `json:"bufferedTransport"`
	BufferedBodyLimit     int64         `json:"bufferedBodyLimit"`
	BufferedEnvelopeLimit int64         `json:"bufferedEnvelopeLimit"`
}

func executeBuild(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("build", flag.ContinueOnError)
	flags.SetOutput(output)
	target := flags.String("target", "native", "native server or Lambda ZIP")
	arch := flags.String("arch", "", "required Lambda architecture: amd64 or arm64")
	out := flags.String("out", "", "production output directory")
	flags.String("addr", "localhost:3000", "reserved development address (unused by build)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("provide one application directory; flags precede the directory")
	}
	if *target != "native" && *target != "lambda" {
		return fmt.Errorf("unknown build target %q", *target)
	}
	archSet, outSet := false, false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "arch" {
			archSet = true
		}
		if f.Name == "out" {
			outSet = true
		}
	})
	if *target == "native" && archSet {
		return fmt.Errorf("-arch is available only with -target lambda")
	}
	if *target == "lambda" && (!archSet || (*arch != "amd64" && *arch != "arm64")) {
		return fmt.Errorf("Lambda requires explicit -arch amd64 or -arch arm64")
	}
	if !outSet {
		if *target == "lambda" {
			*out = "dist/lambda"
		} else {
			*out = "dist/demo"
		}
	}
	if *target == "lambda" && *out == "" {
		return fmt.Errorf("Lambda -out must be nonempty")
	}
	root := flags.Arg(0)
	if *target == "native" {
		build, err := project.Compile(ctx, root, false, output)
		if err != nil {
			return err
		}
		defer os.RemoveAll(build.Dir)
		dir, err := filepath.Abs(*out)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		data, err := os.ReadFile(build.Binary)
		if err != nil {
			return err
		}
		binary := filepath.Join(dir, "server")
		if err = publishBinary(binary, data); err != nil {
			return err
		}
		fmt.Fprintf(output, "Production binary: %s\nRun: %s -addr :3000\n", binary, binary)
		return nil
	}
	dir, err := project.ValidateLambdaOutput(ctx, root, *out)
	if err != nil {
		return err
	}
	placement := func(name string) error { _, err := project.ValidateLambdaOutput(ctx, root, name); return err }
	if err = validatePreviousLambdaChecked(ctx, dir, placement); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		return err
	}
	stage, err := lambdaSibling(filepath.Dir(dir), ".nextgo-lambda-")
	if err != nil {
		return err
	}
	if err = placement(stage); err != nil {
		return err
	}
	if err = os.Mkdir(stage, 0700); err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	build, err := project.CompileLambda(ctx, root, *arch, output)
	if err != nil {
		return err
	}
	defer os.RemoveAll(build.Dir)
	manifest, err := packageLambda(ctx, stage, build, *arch)
	if err != nil {
		return err
	}
	if err = publishLambdaChecked(ctx, stage, dir, output, os.Rename, placement); err != nil {
		return err
	}
	for _, asset := range manifest.Assets {
		if asset.IdentityExceedsBufferedLimit {
			fmt.Fprintf(output, "Warning: embedded asset %s is %d bytes; identity delivery exceeds the buffered 4 MiB body limit. Qualify negotiated compression or response streaming separately.\n", asset.EmbeddedPath, asset.Bytes)
		}
	}
	fmt.Fprintf(output, "Lambda package: %s\nDeployment metadata: %s\nBuffered transport: UNVERIFIED (no cloud deployment performed)\n", filepath.Join(dir, "function.zip"), filepath.Join(dir, "deployment.json"))
	return nil
}

func lambdaSibling(parent, prefix string) (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return filepath.Join(parent, prefix+hex.EncodeToString(nonce[:])), nil
}

func inspectLambdaELF(file *os.File, arch string) (lambdaELF, error) {
	var result lambdaELF
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > lambdaMaxBootstrap {
		return result, fmt.Errorf("Lambda bootstrap must be a bounded regular file")
	}
	binary, err := elf.NewFile(file)
	if err != nil {
		return result, fmt.Errorf("Lambda bootstrap is not ELF: %w", err)
	}
	defer binary.Close()
	machine := elf.EM_X86_64
	if arch == "arm64" {
		machine = elf.EM_AARCH64
	} else if arch != "amd64" {
		return result, fmt.Errorf("invalid Lambda architecture")
	}
	if binary.Class != elf.ELFCLASS64 || binary.Data != elf.ELFDATA2LSB || (binary.OSABI != elf.ELFOSABI_NONE && binary.OSABI != elf.ELFOSABI_LINUX) || binary.Machine != machine || (binary.Type != elf.ET_EXEC && binary.Type != elf.ET_DYN) {
		return result, fmt.Errorf("Lambda bootstrap ELF architecture/ABI/type mismatch")
	}
	entry := false
	for _, program := range binary.Progs {
		if program.Type == elf.PT_LOAD && (program.Memsz > ^uint64(0)-program.Vaddr || program.Filesz > program.Memsz || program.Off > uint64(info.Size()) || program.Filesz > uint64(info.Size())-program.Off) {
			return result, fmt.Errorf("invalid Lambda ELF load range")
		}
		if program.Type == elf.PT_INTERP {
			return result, fmt.Errorf("Lambda bootstrap has an ELF interpreter; static linkage required")
		}
		if program.Type == elf.PT_LOAD && program.Flags&elf.PF_X != 0 && binary.Entry >= program.Vaddr && binary.Entry-program.Vaddr < program.Memsz {
			entry = true
		}
	}
	libraries, err := binary.ImportedLibraries()
	if err != nil {
		return result, err
	}
	if len(libraries) != 0 || !entry {
		return result, fmt.Errorf("Lambda bootstrap needs shared libraries or lacks an executable load-range entry point")
	}
	provenance, err := buildinfo.Read(file)
	if err != nil {
		return result, fmt.Errorf("Lambda bootstrap lacks readable Go build provenance: %w", err)
	}
	settings := map[string]string{}
	for _, setting := range provenance.Settings {
		settings[setting.Key] = setting.Value
	}
	isaKey, isaValue := "GOAMD64", "v1"
	if arch == "arm64" {
		isaKey, isaValue = "GOARM64", "v8.0"
	}
	if settings["GOOS"] != "linux" || settings["GOARCH"] != arch || settings["CGO_ENABLED"] != "0" || settings[isaKey] != isaValue {
		return result, fmt.Errorf("Lambda bootstrap actual Go target/CGO/ISA provenance mismatch")
	}
	return lambdaELF{Machine: binary.Machine.String(), Class: binary.Class.String(), Type: binary.Type.String(), Entry: binary.Entry, Static: true, GoVersion: provenance.GoVersion}, nil
}

func lambdaDigest(ctx context.Context, name, publicPath string, maximum int64) (lambdaFile, error) {
	result := lambdaFile{Path: publicPath}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	info, err := os.Lstat(name)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maximum {
		return result, fmt.Errorf("Lambda artifact must be a regular file within byte limits: %s", publicPath)
	}
	file, err := os.Open(name)
	if err != nil {
		return result, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return result, fmt.Errorf("Lambda artifact changed while opening")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, &lambdaReader{ctx: ctx, reader: io.LimitReader(file, maximum+1)})
	if err != nil {
		return result, err
	}
	if n != info.Size() || n > maximum {
		return result, fmt.Errorf("Lambda artifact changed size or exceeded byte limit")
	}
	result.Bytes, result.SHA256 = n, hex.EncodeToString(hash.Sum(nil))
	return result, ctx.Err()
}

type lambdaReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *lambdaReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type lambdaWriter struct {
	writer  io.Writer
	written int64
	maximum int64
}

func (w *lambdaWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.maximum-w.written {
		return 0, fmt.Errorf("Lambda ZIP exceeds %d byte packaging limit", w.maximum)
	}
	n, err := w.writer.Write(p)
	w.written += int64(n)
	return n, err
}

func packageLambda(ctx context.Context, stage string, build *project.Build, arch string) (*lambdaManifest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if build == nil || build.ID == "" || build.Fingerprint == "" {
		return nil, fmt.Errorf("missing Lambda build identity")
	}
	bootstrap, err := lambdaDigest(ctx, build.Binary, "bootstrap", lambdaMaxBootstrap)
	if err != nil {
		return nil, err
	}
	binary, err := os.Open(build.Binary)
	if err != nil {
		return nil, err
	}
	defer binary.Close()
	observed, err := inspectLambdaELF(binary, arch)
	if err != nil {
		return nil, err
	}
	if _, err = binary.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	archiveName := filepath.Join(stage, "function.zip")
	archive, err := os.OpenFile(archiveName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return nil, err
	}
	writer := zip.NewWriter(&lambdaWriter{writer: archive, maximum: lambdaMaxZIP})
	header := &zip.FileHeader{Name: "bootstrap", Method: zip.Deflate}
	header.SetMode(0755)
	header.SetModTime(time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC))
	entry, copyErr := writer.CreateHeader(header)
	if copyErr == nil {
		var n int64
		n, copyErr = io.Copy(entry, &lambdaReader{ctx: ctx, reader: io.LimitReader(binary, lambdaMaxBootstrap+1)})
		if copyErr == nil && n != bootstrap.Bytes {
			copyErr = fmt.Errorf("bootstrap changed during ZIP creation")
		}
	}
	zipErr, closeErr := writer.Close(), archive.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if zipErr != nil {
		return nil, zipErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	archiveIdentity, err := lambdaDigest(ctx, archiveName, "function.zip", lambdaMaxZIP)
	if err != nil {
		return nil, err
	}
	if err = validateLambdaZIP(ctx, archiveName, bootstrap); err != nil {
		return nil, err
	}
	assets := []lambdaAsset{}
	var assetBytes int64
	visited := 0
	assetRoot := filepath.Join(build.Dir, "server", "assets")
	err = filepath.WalkDir(assetRoot, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > 16384 {
			return fmt.Errorf("Lambda asset inventory exceeds visited-entry limit")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("Lambda assets cannot contain symlinks")
		}
		if entry.IsDir() {
			return nil
		}
		if len(assets) >= lambdaMaxAssets {
			return fmt.Errorf("Lambda asset inventory exceeds %d files", lambdaMaxAssets)
		}
		rel, err := filepath.Rel(assetRoot, name)
		if err != nil {
			return err
		}
		if len(rel) > 512 {
			return fmt.Errorf("Lambda asset path exceeds 512 bytes")
		}
		embedded := filepath.ToSlash(rel)
		if !utf8.ValidString(embedded) {
			return fmt.Errorf("Lambda asset paths must be valid UTF-8 for deployment metadata")
		}
		identity, err := lambdaDigest(ctx, name, embedded, lambdaMaxBootstrap-assetBytes)
		if err != nil {
			return err
		}
		assetBytes += identity.Bytes
		defaultRequest := ""
		if strings.HasPrefix(embedded, "public/") {
			defaultRequest = "/assets/" + strings.TrimPrefix(embedded, "public/")
		}
		if strings.HasPrefix(embedded, "_nextgo/") {
			defaultRequest = "/" + embedded
		}
		assets = append(assets, lambdaAsset{EmbeddedPath: embedded, DefaultRequestPath: defaultRequest, SHA256: identity.SHA256, Bytes: identity.Bytes, IdentityExceedsBufferedLimit: identity.Bytes > lambdaBufferedBody})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].EmbeddedPath < assets[j].EmbeddedPath })
	awsArch, isa := "x86_64", "GOAMD64=v1"
	if arch == "arm64" {
		awsArch, isa = "arm64", "GOARM64=v8.0"
	}
	manifest := &lambdaManifest{Format: "nextgo-lambda-v1", Runtime: "provided.al2023", Handler: "bootstrap", Architecture: awsArch, GOOS: "linux", GOARCH: arch, CGOEnabled: "0", ISABaseline: isa, RuntimeID: build.ID, SourceFingerprint: build.Fingerprint, Bootstrap: bootstrap, Archive: archiveIdentity, ELF: observed, Assets: assets, BufferedTransport: "UNVERIFIED", BufferedBodyLimit: lambdaBufferedBody, BufferedEnvelopeLimit: 6 << 20}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if len(data) > lambdaMaxManifest {
		return nil, fmt.Errorf("Lambda deployment metadata exceeds 1 MiB")
	}
	if err = os.WriteFile(filepath.Join(stage, "deployment.json"), data, 0644); err != nil {
		return nil, err
	}
	return manifest, ctx.Err()
}

func validateLambdaZIP(ctx context.Context, name string, expected lambdaFile) error {
	return validateLambdaZIPActual(ctx, name, expected, "", nil, nil)
}

func validateLambdaZIPActual(ctx context.Context, name string, expected lambdaFile, arch string, observed *lambdaELF, placement func(string) error) (resultErr error) {
	archive, err := zip.OpenReader(name)
	if err != nil {
		return err
	}
	defer archive.Close()
	if len(archive.File) != 1 {
		return fmt.Errorf("Lambda ZIP must contain exactly bootstrap")
	}
	file := archive.File[0]
	if file.Name != "bootstrap" || !file.Mode().IsRegular() || file.Mode().Perm() != 0755 || file.UncompressedSize64 != uint64(expected.Bytes) || file.UncompressedSize64 > lambdaMaxBootstrap {
		return fmt.Errorf("Lambda ZIP bootstrap name/mode/size mismatch")
	}
	body, err := file.Open()
	if err != nil {
		return err
	}
	defer body.Close()
	hash := sha256.New()
	var destination io.Writer = hash
	var inspection *os.File
	if observed != nil {
		inspectionName, nameErr := lambdaSibling(filepath.Dir(filepath.Dir(name)), ".nextgo-lambda-elf-")
		if nameErr != nil {
			return nameErr
		}
		if placement != nil {
			if err := placement(inspectionName); err != nil {
				return err
			}
		}
		inspection, err = os.OpenFile(inspectionName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		defer func() {
			closeErr := inspection.Close()
			removeErr := os.Remove(inspection.Name())
			if closeErr != nil {
				resultErr = errors.Join(resultErr, closeErr)
			}
			if removeErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("Lambda ELF inspection cleanup failed at %s: %w", inspection.Name(), removeErr))
			}
		}()
		destination = io.MultiWriter(hash, inspection)
	}
	n, err := io.Copy(destination, &lambdaReader{ctx: ctx, reader: io.LimitReader(body, lambdaMaxBootstrap+1)})
	if err != nil {
		return err
	}
	if n != expected.Bytes || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return fmt.Errorf("Lambda ZIP bootstrap hash/size mismatch")
	}
	if observed != nil {
		actual, err := inspectLambdaELF(inspection, arch)
		if err != nil {
			return err
		}
		if actual != *observed {
			return fmt.Errorf("actual previous ZIP ELF/build provenance differs from deployment metadata")
		}
	}
	return ctx.Err()
}

func validatePreviousLambda(ctx context.Context, dir string) error {
	return validatePreviousLambdaChecked(ctx, dir, nil)
}

func validatePreviousLambdaChecked(ctx context.Context, dir string, placement func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Lambda output must be a real directory")
	}
	folder, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer folder.Close()
	entries, err := folder.Readdirnames(3)
	if err != nil && err != io.EOF {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	sort.Strings(entries)
	if len(entries) != 2 || strings.Join(entries, ",") != "deployment.json,function.zip" {
		return fmt.Errorf("refusing to replace unidentified nonempty Lambda output")
	}
	identity, err := lambdaDigest(ctx, filepath.Join(dir, "deployment.json"), "deployment.json", lambdaMaxManifest)
	if err != nil {
		return err
	}
	if identity.Bytes == 0 {
		return fmt.Errorf("empty Lambda deployment contract")
	}
	manifestFile, err := os.Open(filepath.Join(dir, "deployment.json"))
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(&lambdaReader{ctx: ctx, reader: io.LimitReader(manifestFile, lambdaMaxManifest+1)})
	closeErr := manifestFile.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if len(data) > lambdaMaxManifest {
		return fmt.Errorf("Lambda metadata exceeded byte limit")
	}
	checkHash := sha256.Sum256(data)
	if hex.EncodeToString(checkHash[:]) != identity.SHA256 {
		return fmt.Errorf("Lambda metadata changed while reading")
	}
	if err := wirejson.Unique(data); err != nil {
		return err
	}
	var manifest lambdaManifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&manifest); err != nil {
		return err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("invalid Lambda deployment contract")
	}
	if manifest.Format != "nextgo-lambda-v1" || manifest.Runtime != "provided.al2023" || manifest.Handler != "bootstrap" || manifest.Bootstrap.Path != "bootstrap" || manifest.Archive.Path != "function.zip" || (manifest.GOARCH != "amd64" && manifest.GOARCH != "arm64") || manifest.RuntimeID == "" || manifest.SourceFingerprint == "" || !manifest.ELF.Static {
		return fmt.Errorf("unrecognized Lambda package contract")
	}
	awsArch, isa := "x86_64", "GOAMD64=v1"
	if manifest.GOARCH == "arm64" {
		awsArch, isa = "arm64", "GOARM64=v8.0"
	}
	if manifest.GOOS != "linux" || manifest.CGOEnabled != "0" || manifest.Architecture != awsArch || manifest.ISABaseline != isa || manifest.BufferedTransport != "UNVERIFIED" || manifest.BufferedBodyLimit != lambdaBufferedBody || manifest.BufferedEnvelopeLimit != 6<<20 || len(manifest.Assets) > lambdaMaxAssets || manifest.ELF.GoVersion == "" {
		return fmt.Errorf("inconsistent previous Lambda package contract")
	}
	actual, err := lambdaDigest(ctx, filepath.Join(dir, "function.zip"), "function.zip", lambdaMaxZIP)
	if err != nil {
		return err
	}
	if actual != manifest.Archive {
		return fmt.Errorf("previous Lambda ZIP does not match its contract")
	}
	return validateLambdaZIPActual(ctx, filepath.Join(dir, "function.zip"), manifest.Bootstrap, manifest.GOARCH, &manifest.ELF, placement)
}

func publishLambda(ctx context.Context, stage, destination string, output io.Writer) error {
	return publishLambdaWithRename(ctx, stage, destination, output, os.Rename)
}

func publishLambdaWithRename(ctx context.Context, stage, destination string, output io.Writer, rename func(string, string) error) error {
	return publishLambdaChecked(ctx, stage, destination, output, rename, nil)
}

func publishLambdaChecked(ctx context.Context, stage, destination string, output io.Writer, rename func(string, string) error, placement func(string) error) error {
	if placement != nil {
		if err := placement(stage); err != nil {
			return err
		}
		if err := placement(destination); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := lambdaDigest(ctx, filepath.Join(stage, "deployment.json"), "deployment.json", lambdaMaxManifest); err != nil {
		return err
	}
	if err := validatePreviousLambdaChecked(ctx, stage, placement); err != nil {
		return err
	}
	if err := validatePreviousLambdaChecked(ctx, destination, placement); err != nil {
		return err
	}
	if err := os.Chmod(stage, 0755); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); os.IsNotExist(err) {
		return rename(stage, destination)
	} else if err != nil {
		return err
	}
	backup, err := lambdaSibling(filepath.Dir(destination), ".nextgo-lambda-previous-")
	if err != nil {
		return err
	}
	if placement != nil {
		if err := placement(backup); err != nil {
			return err
		}
	}
	if err = os.Mkdir(backup, 0700); err != nil {
		return err
	}
	if err = os.Remove(backup); err != nil {
		return err
	}
	if err = rename(destination, backup); err != nil {
		return err
	}
	if err = rename(stage, destination); err != nil {
		if rollback := rename(backup, destination); rollback != nil {
			return fmt.Errorf("Lambda publication failed: %v; restore failed: %v (previous output preserved at %s)", err, rollback, backup)
		}
		return err
	}
	// Publication has committed. Failure to remove an owned previous generation
	// must not masquerade as a failed build or delete the new successful output.
	if err = os.RemoveAll(backup); err != nil {
		fmt.Fprintf(output, "Warning: Lambda package published; previous generation cleanup failed at %s: %v\n", backup, err)
	}
	return nil
}
