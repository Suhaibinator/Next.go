package project

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Suhaibinator/Next.go/internal/zonecontract"
)

const ZoneIngressManifestName = "zone-ingress.json"

type ZoneIngressBuild struct {
	*Build
	Manifest     zonecontract.IngressManifest
	BinarySHA256 string
}

func ReadZoneIngressManifest(name string) (zonecontract.IngressManifest, error) {
	data, _, err := readZonesManifest(name)
	if err != nil {
		return zonecontract.IngressManifest{}, err
	}
	return zonecontract.ParseIngressManifest(data)
}

// CompileZoneIngress uses only the explicitly selected anchor's module,
// workspace, flags and Go installation. The application is never discovered,
// generated, imported or executed; the generated entry imports the framework.
func CompileZoneIngress(ctx context.Context, appRoot, manifestPath string, output io.Writer) (_ *ZoneIngressBuild, err error) {
	manifest, err := ReadZoneIngressManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	canonical, _ := json.Marshal(manifest)
	root, err := filepath.Abs(appRoot)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	if _, err := applicationModule(ctx, root); err != nil {
		return nil, err
	}
	if _, _, err := browserBuildTags(ctx, root); err != nil {
		return nil, err
	}
	fingerprint, err := sourceFingerprintContext(ctx, root)
	if err != nil {
		return nil, err
	}
	generations := filepath.Join(root, ".nextgo")
	if err := os.MkdirAll(generations, 0755); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(generations, "build-zone-ingress-")
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(dir)
		}
	}()
	server := filepath.Join(dir, "server")
	if err := os.MkdirAll(server, 0755); err != nil {
		return nil, err
	}
	if err := writeGo(filepath.Join(server, "main.go"), zoneIngressServerSource(canonical)); err != nil {
		return nil, err
	}
	target, overlay, err := generatedEntryOverlay(ctx, root, server, "zone-ingress")
	if err != nil {
		return nil, err
	}
	binary := filepath.Join(dir, "server-bin")
	if err := run(ctx, root, output, nil, "build", "-overlay="+overlay, "-trimpath", "-ldflags=-s -w", "-o", binary, target); err != nil {
		return nil, err
	}
	if err := validateGeneratedVirtualRoot(root); err != nil {
		return nil, err
	}
	finalFingerprint, err := sourceFingerprintContext(ctx, root)
	if err != nil || finalFingerprint != fingerprint {
		return nil, fmt.Errorf("ingress compiler inputs changed during compilation")
	}
	current, err := ReadZoneIngressManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	currentJSON, _ := json.Marshal(current)
	if !bytes.Equal(currentJSON, canonical) {
		return nil, fmt.Errorf("ingress manifest changed during compilation")
	}
	digest, err := nativeArtifactSHA256(ctx, binary)
	if err != nil {
		return nil, err
	}
	success = true
	return &ZoneIngressBuild{Build: &Build{Dir: dir, Binary: binary, Fingerprint: fingerprint}, Manifest: manifest, BinarySHA256: digest}, nil
}

func zoneIngressServerSource(canonical []byte) string {
	return fmt.Sprintf(`package main
import("context";"encoding/json";"flag";"fmt";"log";"net/http";"os";"os/signal";"syscall";"time"
nextgo %q
%q)
func main(){if err:=run();err!=nil{log.Print(err);os.Exit(1)}}
func run()error{
addr:=flag.String("addr",":3000","listen address")
trusted:=flag.Bool("trusted-tls-offload",false,"admit HTTP from an explicitly trusted HTTPS terminator")
timeout:=flag.Duration("timeout",30*time.Second,"maximum duration of one upstream exchange")
requestBytes:=flag.Int64("max-request-bytes",2<<20,"maximum request body bytes")
responseBytes:=flag.Int64("max-response-bytes",8<<20,"maximum response body bytes")
inFlight:=flag.Int("max-inflight",16,"maximum physically active upstream exchanges")
flag.Parse();if flag.NArg()!=0{return fmt.Errorf("ingress server does not accept positional arguments")}
if *addr==""||*timeout<time.Millisecond||*timeout>5*time.Minute||*requestBytes<1||*requestBytes>64<<20||*responseBytes<1||*responseBytes>64<<20||*inFlight<1||*inFlight>1024{return fmt.Errorf("invalid ingress startup options")}
var manifest nextgo.ZoneIngressManifest
if err:=json.Unmarshal([]byte(%q),&manifest);err!=nil{return err}
ctx,stop:=signal.NotifyContext(context.Background(),syscall.SIGINT,syscall.SIGTERM);defer stop()
app,err:=nextgo.NewZoneIngress(ctx,manifest,nextgo.ZoneIngressOptions{Timeout:*timeout,MaxRequestBytes:*requestBytes,MaxResponseBytes:*responseBytes,MaxInFlight:*inFlight,TrustedTLSOffload:*trusted});if err!=nil{return err}
defer func(){drain,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();if err:=app.Close(drain);err!=nil{log.Printf("Next.go ingress drain: %%v",err)}}()
if os.Getenv("AWS_LAMBDA_RUNTIME_API")!=""{
if err:=serverless.Run(ctx,app);err!=nil&&ctx.Err()==nil{return err};return nil}
server:=&http.Server{Addr:*addr,Handler:app,ReadHeaderTimeout:10*time.Second}
shutdownDone:=make(chan struct{})
go func(){<-ctx.Done();shutdown,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();_=server.Shutdown(shutdown);if err:=app.Close(shutdown);err!=nil{log.Printf("Next.go ingress drain: %%v",err)};close(shutdownDone)}()
defer func(){stop();<-shutdownDone}()
log.Printf("Next.go zone ingress listening on %%s",*addr)
if err:=server.ListenAndServe();err!=nil&&err!=http.ErrServerClosed{return err}
<-shutdownDone
return nil
}
`, framework, framework+"/serverless", canonical)
}
