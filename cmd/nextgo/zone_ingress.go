package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/internal/project"
	"github.com/Suhaibinator/Next.go/internal/zonecontract"
	"github.com/Suhaibinator/Next.go/serverless"
)

type zoneIngressRunner func(context.Context, zonecontract.IngressManifest, string, nextgo.ZoneIngressOptions, io.Writer) error
type zoneIngressCompiler func(context.Context, string, string, io.Writer) (*project.ZoneIngressBuild, error)

func executeZoneIngress(ctx context.Context, args []string, output io.Writer) error {
	return executeZoneIngressWith(ctx, args, output, serveZoneIngress)
}

func executeZoneIngressWith(ctx context.Context, args []string, output io.Writer, run zoneIngressRunner) error {
	flags := flag.NewFlagSet("zones ingress", flag.ContinueOnError)
	flags.SetOutput(output)
	manifestPath := flags.String("manifest", "", "complete immutable ingress manifest")
	addr := flags.String("addr", ":3000", "listen address")
	trusted := flags.Bool("trusted-tls-offload", false, "admit HTTP from an explicitly trusted HTTPS terminator")
	timeout := flags.Duration("timeout", 30*time.Second, "maximum duration of one upstream exchange")
	requestBytes := flags.Int64("max-request-bytes", 2<<20, "maximum request body bytes")
	responseBytes := flags.Int64("max-response-bytes", 8<<20, "maximum response body bytes")
	inFlight := flags.Int("max-inflight", 16, "maximum physically active upstream exchanges")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *manifestPath == "" || *addr == "" {
		return fmt.Errorf("usage: nextgo zones ingress -manifest FILE [-addr :3000] [-trusted-tls-offload] [budget flags]")
	}
	if *timeout < time.Millisecond || *timeout > 5*time.Minute || *requestBytes < 1 || *requestBytes > 64<<20 || *responseBytes < 1 || *responseBytes > 64<<20 || *inFlight < 1 || *inFlight > 1024 {
		return fmt.Errorf("invalid zone ingress budgets")
	}
	manifest, err := project.ReadZoneIngressManifest(*manifestPath)
	if err != nil {
		return err
	}
	return run(ctx, manifest, *addr, nextgo.ZoneIngressOptions{Timeout: *timeout, MaxRequestBytes: *requestBytes, MaxResponseBytes: *responseBytes, MaxInFlight: *inFlight, TrustedTLSOffload: *trusted}, output)
}

func serveZoneIngress(ctx context.Context, manifest zonecontract.IngressManifest, addr string, options nextgo.ZoneIngressOptions, output io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	app, err := nextgo.NewZoneIngress(ctx, manifest, options)
	if err != nil {
		return err
	}
	defer func() {
		drain, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = app.Close(drain)
	}()
	if os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" {
		return serverless.Run(ctx, app)
	}
	server := &http.Server{Addr: addr, Handler: app, ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		_ = app.Close(shutdown)
	}()
	defer func() { cancel(); <-done }()
	if _, err := fmt.Fprintf(output, "Next.go zone ingress listening on %s\n", addr); err != nil {
		_ = server.Close()
		return err
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	<-done
	return nil
}

func executeZoneIngressBuild(ctx context.Context, args []string, output io.Writer) error {
	return executeZoneIngressBuildWith(ctx, args, output, project.CompileZoneIngress)
}

func executeZoneIngressBuildWith(ctx context.Context, args []string, output io.Writer, compile zoneIngressCompiler) error {
	flags := flag.NewFlagSet("zones ingress-build", flag.ContinueOnError)
	flags.SetOutput(output)
	app := flags.String("app", "", "explicit application/module anchor selecting the Go SDK and framework dependency")
	out := flags.String("out", "", "new directory for standalone ingress server and zone-ingress.json")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 || *app == "" || *out == "" {
		return fmt.Errorf("usage: nextgo zones ingress-build -out DIR -app APPROOT <ingress.json>")
	}
	dir, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		if err != nil {
			return err
		}
		return fmt.Errorf("zone ingress output must be a new directory")
	}
	build, err := compile(ctx, *app, flags.Arg(0), output)
	if err != nil {
		return err
	}
	if build == nil || build.Build == nil {
		return fmt.Errorf("zone ingress compiler returned no build")
	}
	defer os.RemoveAll(build.Dir)
	manifest, err := zonecontract.NormalizeIngressManifest(build.Manifest)
	if err != nil {
		return err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := publishNativeContract(ctx, dir, build.Binary, project.ZoneIngressManifestName, data, build.BinarySHA256); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "Zone ingress binary: %s\nManifest: %s\n", filepath.Join(dir, "server"), filepath.Join(dir, project.ZoneIngressManifestName))
	return err
}
