package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Suhaibinator/Next.go/internal/project"
	"github.com/Suhaibinator/Next.go/internal/zonecontract"
)

type zoneReleaseCompiler func(context.Context, string, string, string, string, io.Writer) (*project.ZoneReleaseBuild, error)

func executeZoneRelease(ctx context.Context, args []string, output io.Writer) error {
	return executeZoneReleaseWith(ctx, args, output, project.CompileZoneRelease)
}

func executeZoneReleaseWith(ctx context.Context, args []string, output io.Writer, compile zoneReleaseCompiler) error {
	flags := flag.NewFlagSet("zones release", flag.ContinueOnError)
	flags.SetOutput(output)
	zone := flags.String("zone", "", "exact declared zone ID to compile independently")
	origin := flags.String("public-origin", "", "canonical public scheme and authority")
	release := flags.String("release", "", "immutable release ID; default random 128-bit identity")
	out := flags.String("out", "", "new output directory for server and zone-release.json")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 || *zone == "" || *origin == "" || *out == "" {
		return fmt.Errorf("usage: nextgo zones release -zone ID -public-origin https://example.test [-release A1] -out DIR <zones.json>")
	}
	dir, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		if err != nil {
			return err
		}
		return fmt.Errorf("zone release output must be a new directory")
	}
	build, err := compile(ctx, flags.Arg(0), *zone, *origin, *release, output)
	if err != nil {
		return err
	}
	if build == nil || build.Build == nil {
		return fmt.Errorf("zone release compiler returned no build")
	}
	defer os.RemoveAll(build.Dir)
	if err := publishZoneRelease(ctx, dir, build); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "Zone release %s: %s\nManifest: %s\n", build.Manifest.Release.ReleaseID, filepath.Join(dir, "server"), filepath.Join(dir, project.ZoneReleaseManifestName))
	return err
}

// Publish the binary and its contract together as one new immutable directory.
// Existing output is refused so readers can never observe mixed releases.
func publishZoneRelease(ctx context.Context, dir string, build *project.ZoneReleaseBuild) error {
	manifest, err := zonecontract.NormalizeReleaseManifest(build.Manifest)
	if err != nil {
		return err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return publishNativeContract(ctx, dir, build.Binary, project.ZoneReleaseManifestName, data, manifest.BinarySHA256)
}

func publishNativeContract(ctx context.Context, dir, binary, manifestName string, data []byte, expectedSHA256 string) error {
	if len(data) == 0 || len(data) > zonecontract.MaxManifestBytes {
		return fmt.Errorf("published zone manifest exceeds its 1 MiB wire budget")
	}
	file, err := os.Open(binary)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<20 {
		return fmt.Errorf("zone release binary exceeds artifact bounds")
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dir), ".nextgo-zone-release-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	target, err := os.OpenFile(filepath.Join(stage, "server"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	hash := sha256.New()
	reader := &releaseContextReader{ctx: ctx, reader: io.LimitReader(file, info.Size()+1)}
	n, copyErr := io.Copy(io.MultiWriter(target, hash), reader)
	closeErr := target.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n != info.Size() || hex.EncodeToString(hash.Sum(nil)) != expectedSHA256 {
		return fmt.Errorf("zone release binary does not match its manifest")
	}
	if err := os.WriteFile(filepath.Join(stage, manifestName), data, 0644); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		return fmt.Errorf("zone release output appeared during compilation")
	}
	if err := os.Chmod(stage, 0755); err != nil {
		return err
	}
	return os.Rename(stage, dir)
}

type releaseContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *releaseContextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}
