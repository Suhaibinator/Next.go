package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/project"
)

func executeAnalyze(ctx context.Context, args []string) error {
	return executeAnalyzeWith(ctx, args, os.Stdout, os.Stderr, project.Compile)
}

func executeAnalyzeWith(ctx context.Context, args []string, output, progress io.Writer, compile func(context.Context, string, bool, io.Writer) (*project.Build, error)) error {
	flags := flag.NewFlagSet("analyze", flag.ContinueOnError)
	flags.SetOutput(progress)
	out := flags.String("out", "", "write JSON report atomically to this file instead of stdout")
	runtimeDir := flags.String("runtime-dir", "", "measure an existing runtime namespace without compiling; omit the application argument")
	var budgets project.AnalysisBudgets
	flags.Int64Var(&budgets.CoreBytes, "max-core-bytes", 0, "maximum core WASM bytes (zero disables)")
	flags.Int64Var(&budgets.CoreGzip9Bytes, "max-core-gzip-bytes", 0, "maximum Go gzip-9 core bytes (zero disables)")
	flags.Int64Var(&budgets.LazyBytes, "max-lazy-bytes", 0, "maximum bytes per lazy WASM file (zero disables)")
	flags.Int64Var(&budgets.LazyGzip9Bytes, "max-lazy-gzip-bytes", 0, "maximum Go gzip-9 bytes per lazy file (zero disables)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if (*runtimeDir == "" && flags.NArg() != 1) || (*runtimeDir != "" && flags.NArg() != 0) {
		return fmt.Errorf("analyze requires one application directory or -runtime-dir with no application argument; flags precede the directory")
	}
	if err := budgets.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var report any
	var violations int
	if *runtimeDir != "" {
		if err := analysisOutputOutsideRuntime(*runtimeDir, *out); err != nil {
			return err
		}
		measured, err := project.AnalyzeRuntimeArtifacts(ctx, *runtimeDir, budgets)
		if err != nil {
			return err
		}
		report, violations = measured, len(measured.Violations)
	} else {
		build, err := compile(ctx, flags.Arg(0), false, progress)
		if err != nil {
			return err
		}
		defer os.RemoveAll(build.Dir)
		measured, err := project.AnalyzeBuild(ctx, build, budgets)
		if err != nil {
			return err
		}
		report, violations = measured, len(measured.Violations)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := ctx.Err(); err != nil {
		return err
	}
	if *out == "" {
		_, err = output.Write(data)
	} else {
		err = publishAnalysis(*out, data)
	}
	if err != nil {
		return err
	}
	if violations > 0 {
		return fmt.Errorf("bundle size budget exceeded; see %d violation(s) in analysis report", violations)
	}
	return nil
}

func analysisOutputOutsideRuntime(runtimeDir, out string) error {
	if out == "" {
		return nil
	}
	root, err := filepath.EvalSymlinks(runtimeDir)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(out))
	if err != nil {
		return err
	}
	target, err := filepath.Abs(filepath.Join(parent, filepath.Base(out)))
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("analysis report output must be outside the measured runtime namespace")
	}
	return nil
}

func publishAnalysis(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".nextgo-analysis-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
