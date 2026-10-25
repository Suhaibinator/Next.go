package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A tiny native fixture proves the exporter passes an explicit startup purpose
// to its serving child; no application/WASM build is required.
func TestExportServingChildUsesStaticPurpose(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	binary := filepath.Join(root, "server")
	code := `package main
import("flag";"net/http";"os")
func main(){export:=flag.Bool("static-export",false,"");addr:=flag.String("addr","","");flag.Parse();if !*export{os.Exit(32)};http.HandleFunc("/",func(w http.ResponseWriter,r *http.Request){w.Header().Set("X-Fixture-Phase","static-export");w.Write([]byte("ready"))});http.ListenAndServe(*addr,nil)}
`
	if err := os.WriteFile(source, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, source)
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	base, stop, err := startExportServerEnv(ctx, binary, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("X-Fixture-Phase") != "static-export" {
		t.Fatal(response.StatusCode, response.Header)
	}
}
