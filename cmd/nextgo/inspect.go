package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Suhaibinator/Next.go/internal/dev"
)

func executeInspect(ctx context.Context, args []string) error {
	return executeInspectOutput(ctx, args, os.Stdout)
}

func executeInspectOutput(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	addr := flags.String("addr", "localhost:3000", "local development supervisor address")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("inspect takes no application directory; use -addr localhost:3000")
	}
	snapshot, err := dev.Inspect(ctx, *addr)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(snapshot)
}
