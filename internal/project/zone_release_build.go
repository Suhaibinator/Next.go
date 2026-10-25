package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/releaseassets"
	"github.com/Suhaibinator/Next.go/internal/zonecontract"
)

func (plan *zoneReleasePlan) bindAssets(ctx context.Context, assetsDir, runtimeID string) error {
	_, digest, err := releaseassets.Freeze(ctx, os.DirFS(assetsDir), runtimeID)
	if err != nil {
		return fmt.Errorf("freeze zone release assets: %w", err)
	}
	plan.spec.RuntimeID, plan.spec.AssetsSHA256 = runtimeID, digest
	plan.spec, err = zonecontract.NormalizeReleaseSpec(plan.spec)
	return err
}

func finishZoneRelease(ctx context.Context, build *Build, plan *zoneReleasePlan) (_ *ZoneReleaseBuild, err error) {
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(build.Dir)
		}
	}()
	if err := plan.validateTopology(); err != nil {
		return nil, err
	}
	digest, err := nativeArtifactSHA256(ctx, build.Binary)
	if err != nil {
		return nil, err
	}
	manifest, err := zonecontract.NormalizeReleaseManifest(zonecontract.ReleaseManifest{Release: plan.spec, BinarySHA256: digest})
	if err != nil {
		return nil, err
	}
	success = true
	return &ZoneReleaseBuild{Build: build, Manifest: manifest}, nil
}

func nativeArtifactSHA256(ctx context.Context, name string) (string, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxInputFile {
		return "", fmt.Errorf("native binary exceeds compiler artifact bounds")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, inputReader{ctx, io.LimitReader(file, info.Size()+1)})
	if err != nil || n != info.Size() {
		return "", fmt.Errorf("native binary changed while hashing")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// This entry imports only this application's ordinary native graph. Release
// context is installed by NewZoneRelease before its single configuration hook.
func zoneReleaseServerSource(p *Project, plan *zoneReleasePlan) string {
	imports := serverImports(p)
	keys := make([]string, 0, len(imports))
	for key := range imports {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	aliases := map[string]string{}
	var b strings.Builder
	b.WriteString(`package main
import("embed";"flag";"io/fs";"log";"net/http";"os";"os/signal";"syscall";"context";"time";"encoding/json";"strings";"fmt"
nextgo "` + framework + `"
"` + framework + `/serverless"
"` + framework + `/environment"
`)
	for i, key := range keys {
		alias := fmt.Sprintf("p%d", i)
		aliases[key] = alias
		fmt.Fprintf(&b, "%s %q\n", alias, key)
	}
	b.WriteString(`)
//go:embed all:assets
var embedded embed.FS
func main(){if err:=run();err!=nil{log.Print(err);os.Exit(1)}}
func run()error{
addr:=flag.String("addr",":3000","listen address")
envDir:=flag.String("env-dir",os.Getenv("NEXTGO_ENV_DIR"),"explicit application environment directory")
envMode:=flag.String("env-mode","production","environment mode")
backendHosts:=flag.String("backend-hosts","","trusted exact backend authorities, comma separated")
flag.Parse()
if flag.NArg()!=0{return fmt.Errorf("zone release server does not accept positional arguments")}
if *envDir!=""{if err:=environment.Apply(*envDir,environment.Mode(*envMode));err!=nil{return err}}
explicitBackendHosts:=false;flag.Visit(func(f *flag.Flag){if f.Name=="backend-hosts"{explicitBackendHosts=true}})
if !explicitBackendHosts{*backendHosts=os.Getenv("NEXTGO_ZONE_BACKEND_HOSTS")}
if strings.TrimSpace(*backendHosts)==""{return fmt.Errorf("zone release requires explicit trusted backend hosts")}
hosts:=strings.Split(*backendHosts,",");for i:=range hosts{hosts[i]=strings.TrimSpace(hosts[i])}
assets,err:=fs.Sub(embedded,"assets");if err!=nil{return err}
`)
	writeConfigConstruction(&b, p, aliases, plan.spec.RuntimeID, false)
	b.WriteString("config.Phase=nextgo.PhaseProduction\n")
	encoded, _ := json.Marshal(plan.spec)
	fmt.Fprintf(&b, "var release nextgo.ZoneReleaseSpec\nif err:=json.Unmarshal([]byte(%q),&release);err!=nil{return err}\n", encoded)
	b.WriteString("ctx,stop:=signal.NotifyContext(context.Background(),syscall.SIGINT,syscall.SIGTERM);defer stop()\n")
	fmt.Fprintf(&b, "app,err:=nextgo.NewZoneRelease(ctx,nextgo.ZoneReleaseConfig{Zone:nextgo.ZoneConfig{ID:%q,Host:%q,Tenant:%q,Config:config,Configure:func(config *nextgo.Config)error{\n", plan.selected.ID, plan.selected.Host, plan.selected.Tenant)
	writeMetadataSources(&b, p, aliases)
	if p.Configure != "" {
		fmt.Fprintf(&b, "%s.Configure(config)\n", aliases[p.Configure])
	}
	if len(p.Actions) > 0 {
		fmt.Fprintf(&b, "config.ActionRegistrars=append(config.ActionRegistrars,%s.Register)\n", aliases[p.Import+"/actionrefs"])
	}
	b.WriteString(`return nil}},Release:release,BackendHosts:hosts});if err!=nil{return err}
defer func(){drain,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();if err:=app.Close(drain);err!=nil{log.Printf("Next.go completion drain: %v",err)}}()
if os.Getenv("AWS_LAMBDA_RUNTIME_API")!=""{
if err:=serverless.Run(ctx,app);err!=nil&&ctx.Err()==nil{return err};return nil}
server:=&http.Server{Addr:*addr,Handler:app,ReadHeaderTimeout:10*time.Second}
shutdownDone:=make(chan struct{})
go func(){<-ctx.Done();shutdown,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();_=server.Shutdown(shutdown);if err:=app.Close(shutdown);err!=nil{log.Printf("Next.go completion drain: %v",err)};close(shutdownDone)}()
defer func(){stop();<-shutdownDone}()
log.Printf("Next.go zone release listening on %s",*addr)
if err:=server.ListenAndServe();err!=nil&&err!=http.ErrServerClosed{return err}
<-shutdownDone
return nil
}
`)
	return b.String()
}

// ZoneReleaseManifestName is the portable public contract published beside the
// independently executable native server.
const ZoneReleaseManifestName = "zone-release.json"
