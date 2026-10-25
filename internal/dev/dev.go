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
	listeners      map[chan string]bool
	output         io.Writer
	authority      string
	inspection     inspectionState
}

func Run(ctx context.Context, root, addr string, output io.Writer) error {
	ctx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	s := &supervisor{assets: map[string]string{}, listeners: map[chan string]bool{}, output: output}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.authority = listener.Addr().String()
	server := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	watchDone := make(chan struct{})
	go func() { defer close(watchDone); s.watch(ctx, root) }()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Fprintf(output, "Next.go development: http://%s\n", listener.Addr())
	err = server.Serve(listener)
	cancelRun()
	<-watchDone
	// Shutdown permits ordinary responses to finish, but event streams can live
	// indefinitely. Close those connections when the supervisor itself exits.
	_ = server.Close()
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
		http.Error(w, "unrecognized development host", http.StatusForbidden)
		return
	}
	if r.URL.Path == InspectionPath {
		s.inspect(w, r)
		return
	}

	if r.URL.Path == "/_nextgo/events" {
		s.events(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.mu.Lock()
	proxy := s.proxy
	var assetDir string
	assetPath := r.URL.Path
	// Versioned development assets are public. Keep their generation available
	// even when a new build changes its base/asset mount before a document loads.
	if index := strings.Index(assetPath, "/_nextgo/"); index >= 0 {
		assetPath = assetPath[index:]
		parts := strings.Split(strings.TrimPrefix(assetPath, "/_nextgo/"), "/")
		if len(parts) > 1 {
			assetDir = s.assets[parts[0]]
		}
	}
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
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.listeners, ch); s.mu.Unlock() }()
	_, _ = io.WriteString(w, ": connected\n\n")
	if buildError != "" {
		data, _ := json.Marshal(buildError)
		fmt.Fprintf(w, "event: build-error\ndata: %s\n\n", data)
	}
	if built := r.URL.Query().Get("build"); id != "" && (built != id || r.URL.Query().Get("generation") != generation) {
		_, _ = io.WriteString(w, "event: reload\ndata: \"updated\"\n\n")
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
	generation string
	cmd        *exec.Cmd
	done       chan struct{}
	err        error // published before done closes
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
	cmd := exec.Command(build.Binary, "-addr", addr, "-env-dir", root, "-env-mode", "development", "-dev-generation", generation)
	cmd.Env = append(os.Environ(), "AWS_LAMBDA_RUNTIME_API=")
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
				u, _ := url.Parse("http://" + addr)
				return c, u, nil
			}
		}
	}
}

func (s *supervisor) watch(ctx context.Context, root string) {
	var active *child
	var published *project.Build
	watcher := &project.SourceWatcher{}
	defer func() {
		active.stop()
		s.mu.Lock()
		dirs := s.buildDirs
		s.buildDirs = make(map[string]string)
		s.assets = make(map[string]string)
		s.buildOrder = nil
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
		fingerprint, err := watcher.Fingerprint(ctx, root)
		if err != nil {
			fingerprint += "|error:" + err.Error()
		}
		envFingerprint, envErr := environment.Fingerprint(root, environment.Development)
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
					build, buildErr = project.Compile(ctx, root, true, s.output)
				} else {
					fmt.Fprintln(s.output, "Restarting with updated environment…")
					build = published
				}
				if buildErr == nil {
					// Generation is part of this build. Its published Go source must
					// not trigger a second rebuild/reload on the next poll.
					lastAttempt = build.Fingerprint + "|" + envFingerprint
					var next *child
					var target *url.URL
					next, target, buildErr = start(ctx, build, root, s.output)
					if buildErr == nil {
						current, currentErr := environment.Fingerprint(root, environment.Development)
						if currentErr != nil || current != envFingerprint {
							next.stop()
							buildErr = fmt.Errorf("environment changed during build; rebuilding latest settings")
						}
					}
					if buildErr != nil && compiled {
						_ = os.RemoveAll(build.Dir)
					}
					if buildErr == nil {
						graph := publicInspectionGraph(build.Project)
						proxy := httputil.NewSingleHostReverseProxy(target)
						proxy.ErrorLog = log.New(s.output, "proxy: ", 0)
						s.mu.Lock()
						s.proxy = proxy
						s.id = build.ID
						s.generation = next.generation
						s.buildError = ""
						s.inspection.graph = graph
						s.inspectionTransitionLocked("ready", "")
						expired := s.retainBuildLocked(build.ID, build.Dir)
						s.mu.Unlock()
						previous := active
						active = next
						published = build
						s.publish("reload", build.ID)
						fmt.Fprintln(s.output, "Build ready:", build.ID)
						// Existing responses finish before the previous process exits.
						previous.stop()
						for _, dir := range expired {
							if err := os.RemoveAll(dir); err != nil {
								fmt.Fprintln(s.output, "Remove expired development build:", err)
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
