// Command nextgo creates, develops, builds and exports Go/WASM applications.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Suhaibinator/Next.go/internal/dev"
	"github.com/Suhaibinator/Next.go/internal/project"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "nextgo:", err)
		os.Exit(1)
	}
}
func execute(ctx context.Context, args []string) error {
	const usage = "usage: nextgo init -module example.com/app -framework <source> <new-directory> | nextgo generate [-check] <app> | nextgo dev [-addr localhost:3000] <app> | nextgo inspect [-addr localhost:3000] | nextgo build [-out dist/demo] <app> | nextgo export [-out dist/static] [-paths /,/about] <app> | nextgo zones build [-out dist/zones] <zones.json> | nextgo zones dev [-addr localhost:3000] <zones.json> | nextgo zones release -zone ID -public-origin ORIGIN -out DIR <zones.json> | nextgo zones ingress -manifest FILE | nextgo zones ingress-build -out DIR -app APPROOT <ingress.json>"
	if len(args) == 0 {
		return fmt.Errorf("%s", usage)
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stdout, usage)
		return nil
	}
	switch args[0] {
	case "init", "generate", "export", "dev", "build", "inspect", "zones":
	default:
		return fmt.Errorf("unknown command %q; use nextgo help", args[0])
	}
	if args[0] == "init" {
		return executeInit(ctx, args[1:])
	}
	if args[0] == "export" {
		return executeExport(ctx, args[1:])
	}
	if args[0] == "inspect" {
		return executeInspect(ctx, args[1:])
	}
	if args[0] == "zones" {
		return executeZones(ctx, args[1:])
	}
	if args[0] == "generate" {
		flags := flag.NewFlagSet("generate", flag.ContinueOnError)
		check := flags.Bool("check", false, "check generated routes and action references without writing")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 1 {
			return fmt.Errorf("provide one application directory; flags precede the directory")
		}
		p, err := project.Discover(flags.Arg(0))
		if err != nil {
			return err
		}
		path, err := project.GenerateRoutes(p, *check)
		if err != nil {
			return err
		}
		actionPaths, err := project.GenerateActions(p, *check)
		if err != nil {
			return err
		}
		verb := "Generated"
		if *check {
			verb = "Checked"
		}
		fmt.Fprintf(os.Stdout, "%s typed routes: %s\n", verb, path)
		for _, actionPath := range actionPaths {
			fmt.Fprintf(os.Stdout, "%s action references: %s\n", verb, actionPath)
		}
		return nil
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	addr := flags.String("addr", "localhost:3000", "development listen address")
	out := flags.String("out", "dist/demo", "production output directory")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("provide one application directory; flags precede the directory")
	}
	root := flags.Arg(0)
	switch args[0] {
	case "dev":
		return dev.Run(ctx, root, *addr, os.Stdout)
	case "build":
		build, err := project.Compile(ctx, root, false, os.Stdout)
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
		fmt.Printf("Production binary: %s\nRun: %s -addr :3000\n", binary, binary)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func publishBinary(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".nextgo-server-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if _, err = temp.Write(data); err != nil {
		return err
	}
	if err = temp.Chmod(0755); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
