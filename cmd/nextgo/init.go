package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/Suhaibinator/Next.go/internal/scaffold"
	"os"
)

func executeInit(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	module := flags.String("module", "", "new application's Go module path")
	framework := flags.String("framework", "", "local Next.go framework source directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("provide a new destination directory; flags precede the directory")
	}
	return scaffold.Create(ctx, scaffold.Options{Directory: flags.Arg(0), Module: *module, Framework: *framework}, os.Stdout)
}
