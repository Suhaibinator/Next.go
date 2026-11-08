package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/project"
)

func analysisCommandFixture(t *testing.T) *project.Build {
	t.Helper()
	const id = "0123456789abcdef0123"
	dir := t.TempDir()
	root := filepath.Join(dir, "server", "assets", "_nextgo", id)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"app.wasm": {0, 'a', 's', 'm', 1, 0, 0, 0}, "boot.js": []byte("boot"), "wasm_exec.js": []byte("shim")} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return &project.Build{Dir: dir, ID: id, Binary: binary, Project: &project.Project{}}
}

func TestAnalyzeCommandEmitsReportBeforeBudgetFailureAndCleansGeneration(t *testing.T) {
	build := analysisCommandFixture(t)
	var output, progress bytes.Buffer
	err := executeAnalyzeWith(context.Background(), []string{"-max-core-bytes", "1", "app"}, &output, &progress,
		func(ctx context.Context, root string, dev bool, logs io.Writer) (*project.Build, error) {
			if root != "app" || dev || logs != &progress {
				t.Fatal("analysis changed production compilation or mixed diagnostics with JSON")
			}
			io.WriteString(logs, "compiler progress\n")
			return build, nil
		})
	if err == nil || !strings.Contains(err.Error(), "budget exceeded") {
		t.Fatal("missing budget exit error", err)
	}
	var report project.Analysis
	if err := json.Unmarshal(output.Bytes(), &report); err != nil || len(report.Violations) != 1 || report.Violations[0].Actual != 8 {
		t.Fatal("failed budget did not retain machine-readable evidence", err, output.String())
	}
	if _, err := os.Stat(build.Dir); !os.IsNotExist(err) {
		t.Fatal("analysis retained generated executable/source", err)
	}
}

func TestAnalyzeCommandRejectsInvalidRequestsBeforeCompilation(t *testing.T) {
	compile := func(context.Context, string, bool, io.Writer) (*project.Build, error) {
		t.Fatal("invalid request compiled application code")
		return nil, errors.New("unreachable")
	}
	for _, args := range [][]string{nil, {"one", "two"}, {"-runtime-dir", "existing", "app"}, {"-runtime-dir", "existing", "-max-core-bytes", "-1"}, {"-max-core-bytes", "-1", "app"}, {"-max-lazy-gzip-bytes", "-1", "app"}, {"-unknown", "app"}} {
		if err := executeAnalyzeWith(context.Background(), args, io.Discard, io.Discard, compile); err == nil {
			t.Fatal("invalid analysis request accepted", args)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := executeAnalyzeWith(ctx, []string{"app"}, io.Discard, io.Discard, compile); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestAnalyzeExistingRuntimeNeverCompilesOrDeletesAndRetainsRejectedReport(t *testing.T) {
	build := analysisCommandFixture(t)
	runtimeDir := filepath.Join(build.Dir, "server", "assets", "_nextgo", build.ID)
	out := filepath.Join(t.TempDir(), "report.json")
	compile := func(context.Context, string, bool, io.Writer) (*project.Build, error) {
		t.Fatal("existing artifact mode compiled application code")
		return nil, errors.New("unreachable")
	}
	err := executeAnalyzeWith(context.Background(), []string{"-runtime-dir", runtimeDir, "-out", out, "-max-core-bytes", "1"}, io.Discard, io.Discard, compile)
	if err == nil || !strings.Contains(err.Error(), "budget exceeded") {
		t.Fatal("missing budget failure", err)
	}
	data, err := os.ReadFile(out)
	var report project.RuntimeAnalysis
	if err != nil || json.Unmarshal(data, &report) != nil || len(report.Violations) != 1 || len(report.Assets) != 3 {
		t.Fatal("rejected artifact report unavailable", err, string(data))
	}
	if _, err := os.Stat(runtimeDir); err != nil {
		t.Fatal("artifact mode removed caller-owned generation", err)
	}
	for _, path := range []string{filepath.Join(runtimeDir, "report.json"), filepath.Join(runtimeDir, "app.wasm")} {
		err := executeAnalyzeWith(context.Background(), []string{"-runtime-dir", runtimeDir, "-out", path}, io.Discard, io.Discard, compile)
		if err == nil || !strings.Contains(err.Error(), "outside") {
			t.Fatal("accepted report inside measured artifacts", err)
		}
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(runtimeDir, alias); err != nil {
		t.Fatal(err)
	}
	if err := analysisOutputOutsideRuntime(runtimeDir, filepath.Join(alias, "report.json")); err == nil {
		t.Fatal("report parent alias bypassed input ownership")
	}
	var stdout bytes.Buffer
	if err := executeAnalyzeWith(context.Background(), []string{"-runtime-dir", runtimeDir}, &stdout, io.Discard, compile); err != nil || !json.Valid(stdout.Bytes()) {
		t.Fatal("artifact stdout failed", err, stdout.String())
	}
	data, err = os.ReadFile(filepath.Join(runtimeDir, "app.wasm"))
	if err != nil || !bytes.Equal(data, []byte{0, 'a', 's', 'm', 1, 0, 0, 0}) {
		t.Fatal("caller-owned runtime was altered", err, data)
	}
}

func TestAnalyzeCommandAtomicallyPublishesFileAndCleansAfterWriteFailure(t *testing.T) {
	for _, failure := range []bool{false, true} {
		build := analysisCommandFixture(t)
		out := filepath.Join(t.TempDir(), "report.json")
		if failure {
			out = filepath.Join(out, "missing", "report.json")
		} else if err := os.WriteFile(out, []byte("old report"), 0600); err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		err := executeAnalyzeWith(context.Background(), []string{"-out", out, "app"}, &stdout, io.Discard,
			func(context.Context, string, bool, io.Writer) (*project.Build, error) { return build, nil })
		if (err != nil) != failure || stdout.Len() != 0 {
			t.Fatal("analysis file publication contract", err, stdout.String())
		}
		if !failure {
			data, err := os.ReadFile(out)
			if err != nil || !json.Valid(data) {
				t.Fatal("report was not atomically replaced", err)
			}
		}
		if _, err := os.Stat(build.Dir); !os.IsNotExist(err) {
			t.Fatal("analysis write failure retained a generation", err)
		}
	}
}
