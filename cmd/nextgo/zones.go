package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Suhaibinator/Next.go/internal/dev"
	"github.com/Suhaibinator/Next.go/internal/project"
)

type zonesCompiler func(context.Context, string, bool, io.Writer) (*project.Build, error)
type zonesDeveloper func(context.Context, string, string, io.Writer) error

func executeZones(ctx context.Context, args []string) error {
	return executeZonesWith(ctx, args, os.Stdout, project.CompileZones, dev.RunZones)
}

func executeZonesWith(ctx context.Context, args []string, output io.Writer, compile zonesCompiler, develop zonesDeveloper) error {
	const usage = "usage: nextgo zones build [-out dist/zones] <zones.json> | nextgo zones dev [-addr localhost:3000] <zones.json>"
	if len(args) == 0 {
		return fmt.Errorf("%s", usage)
	}
	switch args[0] {
	case "help", "-h", "--help":
		_, err := fmt.Fprintln(output, usage)
		return err
	case "export":
		return fmt.Errorf("aggregate zones export is not supported; build a standalone zones server")
	case "build", "dev":
	default:
		return fmt.Errorf("unknown zones command %q; use nextgo zones help", args[0])
	}
	flags := flag.NewFlagSet("zones "+args[0], flag.ContinueOnError)
	flags.SetOutput(output)
	var out, addr *string
	if args[0] == "build" {
		out = flags.String("out", "dist/zones", "output directory for the standalone zones server")
	} else {
		addr = flags.String("addr", "localhost:3000", "development listen address; zone Hosts must match or use NEXTGO_ZONE_HOSTS")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("provide one zones manifest; flags precede the manifest")
	}
	if args[0] == "dev" {
		if *addr == "" {
			return fmt.Errorf("provide a nonempty zones development listen address")
		}
		return develop(ctx, flags.Arg(0), *addr, output)
	}
	if *out == "" {
		return fmt.Errorf("provide a nonempty zones output directory")
	}
	dir, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	build, err := compile(ctx, flags.Arg(0), false, output)
	if err != nil {
		return err
	}
	if build == nil {
		return fmt.Errorf("zones compiler returned no build")
	}
	defer os.RemoveAll(build.Dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(build.Binary)
	if err != nil {
		return err
	}
	binary := filepath.Join(dir, "server")
	if err := publishBinary(binary, data); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "Zones production binary: %s\n", binary)
	return err
}
