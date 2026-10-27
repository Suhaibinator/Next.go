package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/project"
	"github.com/Suhaibinator/Next.go/serverless"
)

func TestLambdaCLIRejectsInvalidTargetBeforeDiscovery(t *testing.T) {
	for _, flags := range [][]string{{"-target", "other"}, {"-target", "lambda"}, {"-target", "lambda", "-arch", "x86_64"}, {"-target", "native", "-arch", "amd64"}, {"-target", "lambda", "-arch", "amd64", "-out", ""}} {
		var output bytes.Buffer
		if err := executeBuild(context.Background(), append(flags, filepath.Join(t.TempDir(), "absent")), &output); err == nil {
			t.Fatal("invalid build flags accepted", flags)
		}
	}
}

func TestLambdaGeneratedZIPArchitecturesAndRuntime(t *testing.T) {
	frameworkRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod": fmt.Sprintf("module example.com/lambdaartifact\n\ngo 1.24\nrequire github.com/Suhaibinator/Next.go v0.0.0\nreplace github.com/Suhaibinator/Next.go => %s\n", strconv.Quote(frameworkRoot)),
		"app/page.go": `//go:build !js
package app
import(nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/ui")
func Page(*nextgo.Context)(nextgo.Page,error){return nextgo.Page{Title:"Lambda artifact",Body:ui.El("main",nil,ui.Text("Useful Lambda artifact HTML"))},nil}
`,
		"app/api/health/route.go": `//go:build !js
package health
import "net/http"
func Handle(w http.ResponseWriter,r *http.Request){w.Header().Set("Content-Type","application/json");w.Write([]byte("{\"ok\":true}"))}
`,
		"app/api/oversize/route.go": `//go:build !js
package oversize
import("bytes";"net/http")
func Handle(w http.ResponseWriter,r *http.Request){w.Header().Set("Content-Type","text/plain");w.Write(bytes.Repeat([]byte("x"),(4<<20)+1))}
`,
	}
	for name, data := range files {
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "public"), 0755); err != nil {
		t.Fatal(err)
	}
	proof := bytes.Repeat([]byte("p"), (4<<20)+1)
	if err := os.WriteFile(filepath.Join(root, "public", "proof.txt"), proof, 0644); err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			defer cancel()
			out := filepath.Join(root, "dist", arch)
			var logs bytes.Buffer
			if err := executeBuild(ctx, []string{"-target", "lambda", "-arch", arch, "-out", out, root}, &logs); err != nil {
				t.Fatal(err, logs.String())
			}
			if !strings.Contains(logs.String(), "proof.txt") || !strings.Contains(logs.String(), "UNVERIFIED") {
				t.Fatal("missing transport-size warning", logs.String())
			}
			data, err := os.ReadFile(filepath.Join(out, "deployment.json"))
			if err != nil {
				t.Fatal(err)
			}
			var manifest lambdaManifest
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
			if manifest.GOARCH != arch || manifest.CGOEnabled != "0" || manifest.BufferedTransport != "UNVERIFIED" || manifest.RuntimeID == "" || manifest.SourceFingerprint == "" {
				t.Fatal("missing real build identity", manifest)
			}
			if err := validatePreviousLambda(ctx, out); err != nil {
				t.Fatal(err)
			}
			archive, err := zip.OpenReader(filepath.Join(out, "function.zip"))
			if err != nil {
				t.Fatal(err)
			}
			if len(archive.File) != 1 || archive.File[0].Name != "bootstrap" || archive.File[0].Mode().Perm() != 0755 {
				t.Fatal("ZIP entry contract")
			}
			body, err := archive.File[0].Open()
			if err != nil {
				t.Fatal(err)
			}
			extracted := filepath.Join(t.TempDir(), "bootstrap")
			file, err := os.OpenFile(extracted, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
			if err != nil {
				t.Fatal(err)
			}
			n, copyErr := io.Copy(file, io.LimitReader(body, lambdaMaxBootstrap+1))
			bodyErr, fileErr, zipErr := body.Close(), file.Close(), archive.Close()
			if copyErr != nil || bodyErr != nil || fileErr != nil || zipErr != nil || n != manifest.Bootstrap.Bytes {
				t.Fatal("extract exact bootstrap", copyErr, bodyErr, fileErr, zipErr, n)
			}
			actual, err := lambdaDigest(ctx, extracted, "bootstrap", lambdaMaxBootstrap)
			if err != nil || actual != manifest.Bootstrap {
				t.Fatal("extracted bootstrap identity", actual, err)
			}
			elfFile, err := os.Open(extracted)
			if err != nil {
				t.Fatal(err)
			}
			observed, inspectErr := inspectLambdaELF(elfFile, arch)
			elfFile.Close()
			if inspectErr != nil || observed != manifest.ELF {
				t.Fatal("actual ELF", observed, inspectErr)
			}
			wrong := "arm64"
			if arch == "arm64" {
				wrong = "amd64"
			}
			elfFile, err = os.Open(extracted)
			if err != nil {
				t.Fatal(err)
			}
			_, inspectErr = inspectLambdaELF(elfFile, wrong)
			elfFile.Close()
			if inspectErr == nil {
				t.Fatal("mismatched actual ELF accepted")
			}
			original, err := os.ReadFile(extracted)
			if err != nil {
				t.Fatal(err)
			}
			for _, corruption := range []string{"magic", "entry", "interpreter", "overflow"} {
				mutated := append([]byte(nil), original...)
				switch corruption {
				case "magic":
					mutated[0] = 0
				case "entry":
					binary.LittleEndian.PutUint64(mutated[24:32], 1)
				default:
					offset := binary.LittleEndian.Uint64(mutated[32:40])
					stride := uint64(binary.LittleEndian.Uint16(mutated[54:56]))
					count := int(binary.LittleEndian.Uint16(mutated[56:58]))
					changed := false
					for i := 0; i < count; i++ {
						at := offset + uint64(i)*stride
						if at+56 > uint64(len(mutated)) {
							t.Fatal("actual ELF program header bounds")
						}
						header := mutated[at : at+56]
						if binary.LittleEndian.Uint32(header[:4]) == 1 {
							if corruption == "interpreter" {
								binary.LittleEndian.PutUint32(header[:4], 3)
							} else {
								binary.LittleEndian.PutUint64(header[16:24], ^uint64(0)-1)
								binary.LittleEndian.PutUint64(header[40:48], 16)
							}
							changed = true
							break
						}
					}
					if !changed {
						t.Fatal("actual ELF had no load program")
					}
				}
				broken := filepath.Join(t.TempDir(), "broken")
				if err := os.WriteFile(broken, mutated, 0600); err != nil {
					t.Fatal(err)
				}
				handle, err := os.Open(broken)
				if err != nil {
					t.Fatal(err)
				}
				_, err = inspectLambdaELF(handle, arch)
				handle.Close()
				if err == nil {
					t.Fatal("corrupt actual ELF accepted", corruption)
				}
			}
			// Negative publication uses actual valid artifacts, not a synthetic ELF.
			before, err := os.ReadFile(filepath.Join(out, "function.zip"))
			if err != nil {
				t.Fatal(err)
			}
			empty := filepath.Join(t.TempDir(), "stage")
			if err := os.Mkdir(empty, 0755); err != nil {
				t.Fatal(err)
			}
			if err := publishLambda(ctx, empty, out, &logs); err == nil {
				t.Fatal("empty stage replaced package")
			}
			after, err := os.ReadFile(filepath.Join(out, "function.zip"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("failed publication changed previous bytes", err)
			}
			candidate := t.TempDir()
			for _, name := range []string{"function.zip", "deployment.json"} {
				data, err := os.ReadFile(filepath.Join(out, name))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(candidate, name), data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			attempts := 0
			renameFailure := func(from, to string) error {
				attempts++
				if attempts == 2 {
					return fmt.Errorf("injected publication rename failure")
				}
				return os.Rename(from, to)
			}
			if err := publishLambdaWithRename(ctx, candidate, out, &logs, renameFailure); err == nil || attempts != 3 {
				t.Fatal("rollback not exercised", attempts, err)
			}
			after, err = os.ReadFile(filepath.Join(out, "function.zip"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("rollback changed previous ZIP", err)
			}
			if err := validatePreviousLambda(ctx, out); err != nil {
				t.Fatal("rollback did not restore complete package", err)
			}
			for _, invalidBody := range [][]byte{nil, []byte("not an ELF bootstrap")} {
				impostor := t.TempDir()
				archiveName := filepath.Join(impostor, "function.zip")
				file, err := os.Create(archiveName)
				if err != nil {
					t.Fatal(err)
				}
				writer := zip.NewWriter(file)
				header := &zip.FileHeader{Name: "bootstrap", Method: zip.Deflate}
				header.SetMode(0755)
				entry, err := writer.CreateHeader(header)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = entry.Write(invalidBody); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				fake := manifest
				sum := sha256.Sum256(invalidBody)
				fake.Bootstrap = lambdaFile{Path: "bootstrap", Bytes: int64(len(invalidBody)), SHA256: hex.EncodeToString(sum[:])}
				fake.Archive, err = lambdaDigest(ctx, archiveName, "function.zip", lambdaMaxZIP)
				if err != nil {
					t.Fatal(err)
				}
				metadata, err := json.Marshal(fake)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(impostor, "deployment.json"), metadata, 0644); err != nil {
					t.Fatal(err)
				}
				if err := validateLambdaZIP(ctx, archiveName, fake.Bootstrap); err != nil {
					t.Fatal("negative prior fixture lacks matching ZIP bytes/hash", err)
				}
				priorZIP, err := os.ReadFile(archiveName)
				if err != nil {
					t.Fatal(err)
				}
				if err := validatePreviousLambda(ctx, impostor); err == nil {
					t.Fatal("matched-hash non-ELF prior package recognized")
				}
				leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(impostor), ".nextgo-lambda-elf-*"))
				if err != nil || len(leftovers) != 0 {
					t.Fatal("inspection temporary file remains", leftovers, err)
				}
				if err := publishLambda(ctx, candidate, impostor, &logs); err == nil {
					t.Fatal("matched-hash invalid prior package overwritten")
				}
				after, err := os.ReadFile(archiveName)
				if err != nil || !bytes.Equal(priorZIP, after) {
					t.Fatal("invalid prior ZIP changed", err)
				}
				afterMetadata, err := os.ReadFile(filepath.Join(impostor, "deployment.json"))
				if err != nil || !bytes.Equal(metadata, afterMetadata) {
					t.Fatal("invalid prior metadata changed", err)
				}
			}
			if runtime.GOOS == "linux" && runtime.GOARCH == arch {
				for _, mode := range []string{"buffered", "streaming"} {
					t.Run(mode, func(t *testing.T) { lambdaRuntimeFromZIP(t, extracted, manifest, mode) })
				}
			} else {
				t.Log("real cross-built ELF/ZIP inspected; runtime execution requires Linux and matching test-process architecture", arch)
			}
		})
	}
}

type lambdaTestLogs struct {
	mu       sync.Mutex
	bytes    bytes.Buffer
	overflow bool
}

func (l *lambdaTestLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	remaining := (512 << 10) - l.bytes.Len()
	if remaining < len(p) {
		l.overflow = true
		if remaining < 0 {
			remaining = 0
		}
		p = p[:remaining]
	}
	l.bytes.Write(p)
	return n, nil
}
func (l *lambdaTestLogs) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.bytes.String() }

type lambdaFlight struct {
	path       string
	navigation bool
	encoding   string
	status     int
	asset      *lambdaAsset
	oversize   bool
}

func lambdaRuntimeFromZIP(t *testing.T, bootstrap string, manifest lambdaManifest, mode string) {
	t.Helper()
	childDirectory := t.TempDir()
	var wasm, proof *lambdaAsset
	for i := range manifest.Assets {
		a := &manifest.Assets[i]
		if a.DefaultRequestPath == "/assets/proof.txt" {
			proof = a
		}
		if strings.HasSuffix(a.DefaultRequestPath, "/app.wasm") {
			wasm = a
		}
	}
	if wasm == nil || proof == nil || !proof.IdentityExceedsBufferedLimit {
		t.Fatal("missing actual asset inventory")
	}
	flights := []lambdaFlight{{path: "/", status: 200}, {path: "/", navigation: true, status: 200}, {path: "/api/health", status: 200}, {path: "/missing", status: 404}, {path: proof.DefaultRequestPath, encoding: "gzip", status: 200, asset: proof}, {path: wasm.DefaultRequestPath, encoding: "gzip", status: 200, asset: wasm}, {path: "/api/oversize", encoding: "identity", status: 200, oversize: true}}
	var calls atomic.Int32
	reports := make(chan error, len(flights))
	var posting atomic.Int32
	mockCtx, cancelMock := context.WithCancel(context.Background())
	reportResult := func(err error) {
		select {
		case reports <- err:
		case <-mockCtx.Done():
		}
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/2018-06-01/runtime/invocation/next" {
			index := int(calls.Add(1)) - 1
			if index >= len(flights) {
				select {
				case <-r.Context().Done():
				case <-mockCtx.Done():
				}
				return
			}
			flight := flights[index]
			w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(index))
			w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(10*time.Second).UnixMilli(), 10))
			headers := map[string]string{"host": "package.test", "accept-encoding": flight.encoding}
			if flight.navigation {
				headers["x-nextgo-navigate"] = "1"
			}
			event := serverless.Request{Version: "2.0", RawPath: flight.path, Headers: headers, RequestContext: serverless.RequestContext{DomainName: "package.test", HTTP: serverless.HTTPContext{Method: "GET", Path: flight.path, Protocol: "HTTP/1.1", SourceIP: "127.0.0.1"}}}
			if err := json.NewEncoder(w).Encode(event); err != nil {
				reportResult(err)
			}
			return
		}
		index := int(posting.Add(1)) - 1
		if index >= len(flights) {
			w.WriteHeader(500)
			return
		}
		flight := flights[index]
		want := fmt.Sprintf("/2018-06-01/runtime/invocation/%d/response", index)
		wire, err := io.ReadAll(io.LimitReader(r.Body, (20<<20)+1))
		if err == nil && len(wire) > 20<<20 {
			err = fmt.Errorf("wire bound")
		}
		if err == nil && flight.oversize && mode == "buffered" {
			if r.URL.Path != strings.TrimSuffix(want, "response")+"error" || !bytes.Contains(wire, []byte("buffered Lambda payload limit")) {
				err = fmt.Errorf("actual buffered oversize did not fail through Runtime API: %s", r.URL.Path)
			}
		} else if err == nil {
			if r.URL.Path != want {
				err = fmt.Errorf("unexpected runtime report %s", r.URL.Path)
			} else {
				err = checkLambdaFlight(wire, r, flight, mode, manifest)
			}
		}
		reportResult(err)
		w.WriteHeader(202)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancelMock()
		t.Fatal(err)
	}
	address := listener.Addr().String()
	var connections sync.WaitGroup
	mock := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second, BaseContext: func(net.Listener) context.Context { return mockCtx }, ConnState: func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
		if state == http.StateClosed || state == http.StateHijacked {
			connections.Done()
		}
	}}
	serveDone := make(chan error, 1)
	go func() { serveDone <- mock.Serve(listener) }()
	stopped := false
	var stopErr error
	stopMock := func() error {
		if stopped {
			return stopErr
		}
		stopped = true
		cancelMock()
		// Close stops accepts and force-closes owned connections; unlike
		// httptest.Server.Close it does not wait indefinitely for handlers.
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		closeDone := make(chan error, 1)
		go func() { closeDone <- mock.Close() }()
		var closeErr error
		select {
		case closeErr = <-closeDone:
		case <-deadline.C:
			stopErr = fmt.Errorf("mock Close join deadline")
			return stopErr
		}
		select {
		case err := <-serveDone:
			if err != http.ErrServerClosed {
				stopErr = fmt.Errorf("mock Serve exit: %v", err)
			}
		case <-deadline.C:
			stopErr = fmt.Errorf("mock Serve join deadline")
			return stopErr
		}
		// StateNew runs synchronously in Serve before each connection goroutine.
		// Once Serve is joined, no further Add is possible. StateClosed follows
		// handler return and physical connection close, so this proves both.
		joinedConnections := make(chan struct{})
		go func() { connections.Wait(); close(joinedConnections) }()
		select {
		case <-joinedConnections:
		case <-deadline.C:
			stopErr = fmt.Errorf("mock connection/handler join deadline")
			return stopErr
		}
		if closeErr != nil {
			stopErr = closeErr
		}
		return stopErr
	}
	logs := &lambdaTestLogs{}
	cmd := exec.Command(bootstrap)
	for _, pair := range os.Environ() {
		key, _, _ := strings.Cut(pair, "=")
		if !strings.HasPrefix(key, "AWS_LAMBDA_") && !strings.HasPrefix(key, "NEXTGO_LAMBDA_") && key != "NEXTGO_ENV_DIR" {
			cmd.Env = append(cmd.Env, pair)
		}
	}
	cmd.Env = append(cmd.Env, "AWS_LAMBDA_RUNTIME_API="+address, "NEXTGO_LAMBDA_RESPONSE_MODE="+mode)
	cmd.Dir = childDirectory
	cmd.Stdout = logs
	cmd.Stderr = logs
	done := make(chan error, 1)
	started, joined := false, false
	defer func() {
		cancelMock()
		if started && !joined {
			cmd.Process.Kill()
			select {
			case <-done:
				joined = true
			case <-time.After(5 * time.Second):
				t.Error("owned bootstrap did not join after kill")
			}
		}
		if err := stopMock(); err != nil {
			t.Error(err)
		}
	}()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	started = true
	go func() { done <- cmd.Wait() }()
	for index := range flights {
		if !t.Run(fmt.Sprintf("flight-%02d", index), func(t *testing.T) {
			select {
			case err := <-reports:
				if err != nil {
					t.Fatal(err)
				}
			case err := <-done:
				joined = true
				t.Fatal("bootstrap exited before all real flights", err, logs.String())
			case <-time.After(20 * time.Second):
				t.Fatal("real Runtime API flight deadline", logs.String())
			}
		}) {
			t.FailNow()
		}
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal("bootstrap SIGTERM", err, logs.String())
		}
	case <-time.After(7 * time.Second):
		t.Fatal("bootstrap graceful shutdown deadline")
	}
	if logs.overflow {
		t.Fatal("bootstrap log limit exceeded")
	}
	if err := stopMock(); err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatal("owned Runtime API listener remains open")
	}
}

func checkLambdaFlight(wire []byte, r *http.Request, flight lambdaFlight, mode string, manifest lambdaManifest) error {
	var envelope serverless.Response
	var payload []byte
	if r.Trailer.Get("Lambda-Runtime-Function-Error-Type") != "" || r.Trailer.Get("Lambda-Runtime-Function-Error-Body") != "" {
		return fmt.Errorf("streaming runtime error trailer")
	}
	if mode == "buffered" {
		if len(wire) > 6<<20 {
			return fmt.Errorf("actual buffered JSON exceeded limit")
		}
		if err := json.Unmarshal(wire, &envelope); err != nil {
			return err
		}
		payload = []byte(envelope.Body)
		if envelope.IsBase64Encoded {
			var err error
			payload, err = base64.StdEncoding.DecodeString(envelope.Body)
			if err != nil {
				return err
			}
		}
		if len(payload) > 4<<20 {
			return fmt.Errorf("actual buffered body exceeded limit")
		}
	} else {
		if r.Header.Get("Content-Type") != "application/vnd.awslambda.http-integration-response" || r.Header.Get("Lambda-Runtime-Function-Response-Mode") != "streaming" || len(r.TransferEncoding) != 1 || r.TransferEncoding[0] != "chunked" {
			return fmt.Errorf("actual streaming framing headers")
		}
		separator := bytes.Index(wire, make([]byte, 8))
		if separator < 0 || separator > 16<<10 {
			return fmt.Errorf("streaming prelude delimiter")
		}
		if err := json.Unmarshal(wire[:separator], &envelope); err != nil {
			return err
		}
		payload = wire[separator+8:]
	}
	if envelope.StatusCode != flight.status {
		return fmt.Errorf("HTTP status %d expected %d", envelope.StatusCode, flight.status)
	}
	if flight.asset != nil && envelope.Headers["content-encoding"] != "gzip" {
		return fmt.Errorf("real asset did not negotiate gzip")
	}
	if envelope.Headers["content-encoding"] == "gzip" {
		reader, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return err
		}
		decoded, readErr := io.ReadAll(io.LimitReader(reader, 32<<20))
		closeErr := reader.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if len(decoded) >= 32<<20 {
			return fmt.Errorf("decoded bound")
		}
		payload = decoded
	}
	if flight.asset != nil {
		sum := sha256.Sum256(payload)
		if int64(len(payload)) != flight.asset.Bytes || hex.EncodeToString(sum[:]) != flight.asset.SHA256 {
			return fmt.Errorf("real embedded asset hash mismatch")
		}
		if strings.HasSuffix(flight.path, "/app.wasm") && (!bytes.HasPrefix(payload, []byte{0, 'a', 's', 'm'}) || !strings.Contains(envelope.Headers["cache-control"], "immutable") || !strings.Contains(envelope.Headers["content-type"], "application/wasm")) {
			return fmt.Errorf("WASM immutable identity")
		}
		return nil
	}
	if flight.oversize {
		if len(payload) != (4<<20)+1 {
			return fmt.Errorf("real streaming oversize bytes")
		}
		return nil
	}
	if flight.navigation {
		var nav struct {
			Version, Status    int
			HTML, Build, Title string
		}
		if err := json.Unmarshal(payload, &nav); err != nil {
			return err
		}
		if nav.Version != 3 || nav.Status != 200 || nav.Build != manifest.RuntimeID || !strings.Contains(nav.HTML, "Useful Lambda artifact HTML") {
			return fmt.Errorf("real navigation identity: %+v", nav)
		}
		return nil
	}
	if flight.path == "/" && (!bytes.Contains(payload, []byte("Useful Lambda artifact HTML")) || !bytes.Contains(payload, []byte(manifest.RuntimeID))) {
		return fmt.Errorf("real SSR/runtime identity")
	}
	if flight.path == "/api/health" && !bytes.Contains(payload, []byte(`"ok":true`)) {
		return fmt.Errorf("real API payload")
	}
	return nil
}

func TestLambdaPackageLimitsAndUnknownDirectoryStayFailClosed(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	marker := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(marker, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validatePreviousLambda(ctx, dir); err == nil {
		t.Fatal("unidentified output accepted")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "unrelated" {
		t.Fatal("unrelated bytes changed", err)
	}
	var out bytes.Buffer
	writer := lambdaWriter{writer: &out, maximum: 3}
	if _, err := writer.Write([]byte("four")); err == nil || out.Len() != 0 {
		t.Fatal("ZIP byte bound did not fail before write")
	}
	oversize := filepath.Join(t.TempDir(), "large")
	file, err := os.Create(oversize)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(lambdaMaxBootstrap + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	if _, err := lambdaDigest(ctx, oversize, "bootstrap", lambdaMaxBootstrap); err == nil {
		t.Fatal("bootstrap uncompressed size cap")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := packageLambda(cancelled, t.TempDir(), &project.Build{ID: "x", Fingerprint: "y", Binary: marker}, "amd64"); err == nil {
		t.Fatal("cancelled package accepted")
	}
}
