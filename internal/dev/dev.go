// Package dev supervises complete application builds behind a stable HTTP proxy.
package dev

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Suhaibinator/Next.go/environment"
	"github.com/Suhaibinator/Next.go/internal/devrefresh"
	"github.com/Suhaibinator/Next.go/internal/project"
)

type supervisor struct {
	mu             sync.Mutex
	proxy          *httputil.ReverseProxy
	id, buildError string
	generation     string
	assets         map[string]string
	buildDirs      map[string]string
	buildOrder     []string
	buildAssets    map[string]map[string]string
	buildOwners    map[string]map[string][]assetOwner
	assetOwners    map[string][]assetOwner
	currentOwners  map[string][]assetOwner
	currentIDs     map[string]bool
	refresh        *devrefresh.Manifest
	refreshBaseKey string
	pinnedBuilds   map[string]bool
	listeners      map[chan string]bool
	output         io.Writer
	authority      string
	zoneHosts      map[string]bool
	controlHosts   map[string]bool
	inspection     inspectionState
}

func Run(ctx context.Context, root, addr string, output io.Writer) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	s := &supervisor{assets: map[string]string{}, listeners: map[chan string]bool{}, output: output}
	return runSupervisor(ctx, s, addr, func(ctx context.Context) { s.watch(ctx, root) })
}

func runSupervisor(ctx context.Context, s *supervisor, addr string, watch func(context.Context)) error {
	ctx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.authority = listener.Addr().String()
	if s.zoneHosts != nil {
		s.installControlHosts(addr)
	}
	server := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	watchDone := make(chan struct{})
	shutdownDone := make(chan struct{})
	go func() { defer close(watchDone); watch(ctx) }()
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Fprintf(s.output, "Next.go development: http://%s\n", listener.Addr())
	err = server.Serve(listener)
	cancelRun()
	<-watchDone
	// Shutdown permits ordinary responses to finish, but event streams can live
	// indefinitely. Close those connections when the supervisor itself exits.
	_ = server.Close()
	<-shutdownDone
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Reject unrecognized authorities before exposing build errors, event streams,
// assets, or the application proxy. Loopback binding alone does not prevent DNS
// rebinding from an attacker-controlled browser origin.
func (s *supervisor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.allowedHost(r.Host) {
		s.mu.Lock()
		dir, _ := s.assetRequestLocked(r)
		s.mu.Unlock()
		if dir == "" {
			http.Error(w, "unrecognized development host", http.StatusForbidden)
			return
		}
	}
	if r.URL.Path == InspectionPath {
		s.inspect(w, r)
		return
	}
	if r.URL.Path == devrefresh.ManifestPath {
		s.serveRefresh(w, r)
		return
	}

	if r.URL.Path == "/_nextgo/events" {
		if !s.allowedInspectionRequest(r) {
			http.Error(w, "development events require a local same-origin request", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.events(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.mu.Lock()
	proxy := s.proxy
	assetDir, assetPath := s.assetRequestLocked(r)
	buildError := s.buildError
	s.mu.Unlock()
	if assetDir != "" {
		copy := r.Clone(r.Context())
		copy.URL.Path = assetPath
		copy.URL.RawPath = ""
		http.FileServer(http.Dir(assetDir)).ServeHTTP(w, copy)
		return
	}
	if proxy == nil {
		if !s.allowedInspectionRequest(r) {
			http.Error(w, "development status requires a local same-origin request", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(503)
		message := "Building application…"
		if buildError != "" {
			message = "Build failed: " + buildError + ". Saving a fix rebuilds automatically."
		}
		fmt.Fprintf(w, `<!doctype html><title>Next.go development</title><p id="nextgo-dev-status" role="status">%s</p><script>const events=new EventSource('/_nextgo/events');events.addEventListener('reload',()=>location.reload());events.addEventListener('build-error',e=>{document.getElementById('nextgo-dev-status').textContent='Build failed: '+JSON.parse(e.data)+'. Saving a fix rebuilds automatically.';});</script>`, html.EscapeString(message))
		return
	}
	proxy.ServeHTTP(w, r)
}

func (s *supervisor) allowedHost(authority string) bool {
	s.mu.Lock()
	if s.zoneHosts != nil {
		allowed := s.allowedZoneHostLocked(authority)
		s.mu.Unlock()
		return allowed
	}
	s.mu.Unlock()
	boundHost, boundPort, err := net.SplitHostPort(s.authority)
	if err != nil {
		return false
	}
	host, port, err := net.SplitHostPort(authority)
	if err != nil || port != boundPort {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	boundIP := net.ParseIP(boundHost)
	return boundHost != "" && (boundIP == nil || !boundIP.IsUnspecified()) && strings.EqualFold(host, boundHost)
}

func (s *supervisor) publish(event, value string) {
	data, _ := json.Marshal(value)
	message := "event: " + event + "\ndata: " + string(data) + "\n\n"
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.listeners {
		select {
		case ch <- message:
		default:
		}
	}
}
func (s *supervisor) events(w http.ResponseWriter, r *http.Request) {
	flush, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	ch := make(chan string, 8)
	s.mu.Lock()
	s.listeners[ch] = true
	id, buildError, generation := s.id, s.buildError, s.generation
	serving := s.proxy != nil
	built := r.URL.Query().Get("build")
	current := built == id || s.currentIDs[built]
	if s.zoneHosts != nil {
		current = s.currentIDs[built] && runtimeHostMatches(s.currentOwners[built], r.Host)
	}
	var refreshData []byte
	if serving && current && r.URL.Query().Get("generation") == generation && s.refresh != nil {
		refreshData, _ = json.Marshal(s.refresh)
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.listeners, ch); s.mu.Unlock() }()
	_, _ = io.WriteString(w, ": connected\n\n")
	if buildError != "" {
		data, _ := json.Marshal(buildError)
		fmt.Fprintf(w, "event: build-error\ndata: %s\n\n", data)
	}
	if serving && id != "" && (!current || r.URL.Query().Get("generation") != generation) {
		_, _ = io.WriteString(w, "event: reload\ndata: \"updated\"\n\n")
	}
	if len(refreshData) != 0 {
		_, _ = io.WriteString(w, refreshEvent(refreshData))
	}
	flush.Flush()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case message := <-ch:
			if _, err := io.WriteString(w, message); err != nil {
				return
			}
			flush.Flush()
		case <-ticker.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flush.Flush()
		}
	}
}

type child struct {
	generation   string
	refreshReady bool
	cmd          *exec.Cmd
	done         chan struct{}
	err          error // published before done closes
}

func (c *child) stop() {
	if c == nil {
		return
	}
	_ = c.cmd.Process.Signal(os.Interrupt)
	select {
	case <-c.done:
	case <-time.After(6 * time.Second):
		_ = c.cmd.Process.Kill()
		<-c.done
	}
}

// exited reports a terminal child once; rebuilding remains tied to source edits.
func (s *supervisor) exited(c *child) bool {
	if c == nil {
		return false
	}
	select {
	case <-c.done:
		buildError := fmt.Sprintf("application server exited: %v; save a source file to rebuild", c.err)
		s.mu.Lock()
		s.proxy = nil
		s.currentIDs = nil
		s.refresh = nil
		s.refreshBaseKey = ""
		s.pinnedBuilds = nil
		s.buildError = buildError
		s.inspectionTransitionLocked("failed", "server_exited")
		s.mu.Unlock()
		fmt.Fprintln(s.output, buildError)
		s.publish("build-error", buildError)
		return true
	default:
		return false
	}
}

func start(ctx context.Context, build *project.Build, root string, output io.Writer) (*child, *url.URL, error) {
	return startWithArgs(ctx, build, root, output, nil)
}

func startWithArgs(ctx context.Context, build *project.Build, root string, output io.Writer, extra []string) (*child, *url.URL, error) {
	return startWithEnvironment(ctx, build, root, output, extra, os.Environ())
}

func startWithEnvironment(ctx context.Context, build *project.Build, root string, output io.Writer, extra, ambient []string) (*child, *url.URL, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	addr := l.Addr().String()
	_ = l.Close()
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return nil, nil, err
	}
	generation := hex.EncodeToString(token)
	args := []string{"-addr", addr, "-env-dir", root, "-env-mode", "development", "-dev-generation", generation}
	readyPath := ""
	if build.DevRefresh != nil && build.Zones == nil {
		// The native process creates the final file exclusively after validating
		// its actual application configuration. Failure disables refresh only.
		if file, err := os.CreateTemp(build.Dir, ".dev-ready-*"); err == nil {
			readyPath = file.Name()
			_ = file.Close()
			if err := os.Remove(readyPath); err == nil {
				args = append(args, "-dev-ready", readyPath)
			} else {
				readyPath = ""
			}
		}
	}
	if readyPath != "" {
		defer os.Remove(readyPath)
	}
	cmd := exec.Command(build.Binary, append(args, extra...)...)
	cmd.Env = developmentEnvironment(ambient)
	cmd.Stdout = output
	cmd.Stderr = output
	if err = cmd.Start(); err != nil {
		return nil, nil, err
	}
	c := &child{generation: generation, cmd: cmd, done: make(chan struct{})}
	go func() { c.err = cmd.Wait(); close(c.done) }()
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			c.stop()
			return nil, nil, ctx.Err()
		case <-c.done:
			return nil, nil, fmt.Errorf("server exited during startup: %v", c.err)
		case <-timeout.C:
			c.stop()
			return nil, nil, fmt.Errorf("server startup timed out")
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				c.refreshReady = readDevelopmentReady(readyPath, build.ID, generation)
				u, _ := url.Parse("http://" + addr)
				return c, u, nil
			}
		}
	}
}

func developmentEnvironment(ambient []string) []string {
	return append(append([]string(nil), ambient...), "AWS_LAMBDA_RUNTIME_API=")
}

func (s *supervisor) watch(ctx context.Context, root string) {
	s.watchBuilds(ctx, developmentBuild{
		root: root, watcher: &project.SourceWatcher{},
		compile: func(ctx context.Context) (*project.Build, error) { return project.Compile(ctx, root, true, s.output) },
		start: func(ctx context.Context, build *project.Build, _ *developmentPublication) (*child, *url.URL, error) {
			return start(ctx, build, root, s.output)
		},
	})
}

type sourceWatcher interface {
	Fingerprint(context.Context, string) (string, error)
}

type developmentBuild struct {
	root, source string
	watcher      sourceWatcher
	compile      func(context.Context) (*project.Build, error)
	start        func(context.Context, *project.Build, *developmentPublication) (*child, *url.URL, error)
	validate     func(*project.Build) (*developmentPublication, error)
}

func (s *supervisor) watchBuilds(ctx context.Context, target developmentBuild) {
	var active *child
	var published *project.Build
	publishedEnvironment := ""
	source := target.source
	if source == "" {
		source = target.root
	}
	defer func() {
		active.stop()
		s.mu.Lock()
		dirs := s.buildDirs
		s.buildDirs = make(map[string]string)
		s.assets = make(map[string]string)
		s.buildOrder = nil
		s.buildAssets = nil
		s.buildOwners = nil
		s.assetOwners = nil
		s.currentOwners = nil
		s.currentIDs = nil
		s.proxy = nil
		s.refresh = nil
		s.refreshBaseKey = ""
		s.pinnedBuilds = nil
		s.mu.Unlock()
		for _, dir := range dirs {
			if err := os.RemoveAll(dir); err != nil {
				fmt.Fprintln(s.output, "Remove development build:", err)
			}
		}
	}()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	lastAttempt := ""
	pending := ""
	changed := time.Now()
	for {
		if s.exited(active) {
			active = nil
		}
		fingerprint, err := target.watcher.Fingerprint(ctx, source)
		if err != nil {
			fingerprint += "|error:" + err.Error()
		}
		envFingerprint, envErr := environment.Fingerprint(target.root, environment.Development)
		if envErr != nil {
			envFingerprint = "error:" + envErr.Error()
		}
		sourceFingerprint := fingerprint
		fingerprint += "|" + envFingerprint
		if fingerprint != lastAttempt {
			if pending != fingerprint {
				pending = fingerprint
				changed = time.Now()
			}
			if lastAttempt == "" || time.Since(changed) >= 250*time.Millisecond {
				lastAttempt = fingerprint
				var build *project.Build
				var buildErr error
				s.mu.Lock()
				s.inspectionTransitionLocked("building", "")
				s.mu.Unlock()
				compiled := published == nil || published.Fingerprint != sourceFingerprint
				if compiled {
					fmt.Fprintln(s.output, "Building Go server and WASM…")
					build, buildErr = target.compile(ctx)
				} else {
					fmt.Fprintln(s.output, "Reusing compiled application…")
					build = published
				}
				if buildErr == nil {
					var publication *developmentPublication
					if build == nil {
						buildErr = fmt.Errorf("compiler returned no development build")
					} else if target.validate != nil {
						publication, buildErr = target.validate(build)
					}
					if buildErr != nil {
						if compiled && build != nil {
							_ = os.RemoveAll(build.Dir)
						}
					} else {
						// Generation is part of this build. Its published Go source must
						// not trigger a second rebuild/reload on the next poll.
						lastAttempt = build.Fingerprint + "|" + envFingerprint
						currentSource, sourceErr := target.watcher.Fingerprint(ctx, source)
						currentEnv, environmentErr := environment.Fingerprint(target.root, environment.Development)
						if sourceErr != nil || currentSource != build.Fingerprint || environmentErr != nil || currentEnv != envFingerprint {
							if compiled {
								_ = os.RemoveAll(build.Dir)
							}
							fmt.Fprintln(s.output, "Build superseded; waiting for current inputs…")
						} else if refreshed, expired := s.tryClientRefresh(build, active, published != nil && envErr == nil && envFingerprint == publishedEnvironment); refreshed {
							published = build
							fmt.Fprintln(s.output, "Client refresh ready:", build.ID)
							s.removeExpired(expired)
						} else {
							var next *child
							var destination *url.URL
							next, destination, buildErr = target.start(ctx, build, publication)
							if buildErr == nil {
								current, currentErr := environment.Fingerprint(target.root, environment.Development)
								if currentErr != nil || current != envFingerprint {
									next.stop()
									buildErr = fmt.Errorf("environment changed during build; rebuilding latest settings")
								}
							}
							if buildErr != nil && compiled {
								_ = os.RemoveAll(build.Dir)
							}
							if buildErr == nil {
								graph := publicBuildInspectionGraph(build)
								proxy := httputil.NewSingleHostReverseProxy(destination)
								proxy.ErrorLog = log.New(s.output, "proxy: ", 0)
								s.mu.Lock()
								s.proxy = proxy
								s.id = build.ID
								s.generation = next.generation
								s.buildError = ""
								if publication != nil {
									s.zoneHosts = publication.hosts
									s.currentOwners = publication.owners
								}
								s.inspection.graph = graph
								s.inspectionTransitionLocked("ready", "")
								var owners map[string][]assetOwner
								if publication != nil {
									owners = publication.owners
								}
								retentionID := build.ID
								if publication != nil {
									retentionID = next.generation
								}
								retentionID = s.setServingRefreshLocked(build, next, retentionID)
								expired := s.retainBuildRuntimeLocked(retentionID, build.Dir, developmentAssets(build), owners)
								s.currentIDs = make(map[string]bool)
								for id := range developmentAssets(build) {
									s.currentIDs[id] = true
								}
								s.mu.Unlock()
								previous := active
								active = next
								published = build
								publishedEnvironment = envFingerprint
								s.publish("reload", build.ID)
								fmt.Fprintln(s.output, "Build ready:", build.ID)
								// Existing responses finish before the previous process exits.
								previous.stop()
								s.removeExpired(expired)
							}
						}
					}
				}
				if buildErr != nil && ctx.Err() == nil {
					s.mu.Lock()
					s.buildError = buildErr.Error()
					s.inspectionTransitionLocked("failed", "build_failed")
					s.mu.Unlock()
					fmt.Fprintln(s.output, "Build failed:", buildErr)
					s.publish("build-error", buildErr.Error())
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *supervisor) removeExpired(directories []string) {
	for _, dir := range directories {
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintln(s.output, "Remove expired development build:", err)
		}
	}
}
