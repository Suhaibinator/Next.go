package dev

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Suhaibinator/Next.go/internal/ascii"
	"github.com/Suhaibinator/Next.go/internal/devrefresh"
	"github.com/Suhaibinator/Next.go/internal/project"
)

// Only public module descriptors enter this protocol. Runtime snapshots and
// application state do not belong in the development supervisor.
func refreshManifest(build *project.Build, servingBuild, generation string, revision uint64) *devrefresh.Manifest {
	if build == nil || build.Zones != nil || build.DevRefresh == nil || !ascii.LowerHex(build.ID, 20) {
		return nil
	}
	proof := build.DevRefresh
	for _, module := range proof.Modules {
		if module.URL != "/_nextgo/"+build.ID+"/module-"+module.Digest+".wasm" {
			return nil
		}
	}
	manifest := &devrefresh.Manifest{Version: proof.Version, Revision: revision, ServingBuild: servingBuild, ServingGeneration: generation, Compatibility: proof.Compatibility, ShimDigest: proof.ShimDigest, ModuleABI: proof.ModuleABI, Modules: append([]devrefresh.Module(nil), proof.Modules...)}
	if revision > 0 {
		manifest.Previous = revision - 1
	}
	if manifest.Validate() != nil {
		return nil
	}
	return manifest
}

func nativeRefreshReady(active *child) bool {
	if active == nil || !active.refreshReady {
		return false
	}
	select {
	case <-active.done:
		return false
	default:
		return true
	}
}

func refreshArtifactsAvailable(build *project.Build, manifest *devrefresh.Manifest) bool {
	for _, module := range manifest.Modules {
		path := filepath.Join(build.Dir, "server", "assets", "_nextgo", build.ID, "module-"+module.Digest+".wasm")
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != int64(module.Size) {
			return false
		}
		file, err := os.Open(path)
		if err != nil {
			return false
		}
		hash := sha256.New()
		size, readErr := io.Copy(hash, io.LimitReader(file, int64(module.Size)+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || size != int64(module.Size) || hex.EncodeToString(hash.Sum(nil)) != module.Digest {
			return false
		}
	}
	return true
}

// Caller holds s.mu. The unique child generation prevents an identical build
// ID from replacing/removing the directory of the still-running native binary.
func (s *supervisor) setServingRefreshLocked(build *project.Build, active *child, ordinaryKey string) string {
	s.refresh = nil
	s.refreshBaseKey = ""
	s.pinnedBuilds = nil
	if !nativeRefreshReady(active) {
		return ordinaryKey
	}
	manifest := refreshManifest(build, build.ID, active.generation, 0)
	if manifest == nil || !refreshArtifactsAvailable(build, manifest) {
		return ordinaryKey
	}
	key := "native:" + active.generation
	s.refresh = manifest
	s.refreshBaseKey = key
	s.pinnedBuilds = map[string]bool{key: true}
	return key
}

func (s *supervisor) tryClientRefresh(build *project.Build, active *child, environmentUnchanged bool) (bool, []string) {
	if !environmentUnchanged {
		return false, nil
	}
	return s.publishClientRefresh(build, active)
}

// The caller has checked the compiler snapshot and environment against this
// candidate. The serving native child and core remain pinned and unchanged.
func (s *supervisor) publishClientRefresh(build *project.Build, active *child) (bool, []string) {
	if !nativeRefreshReady(active) {
		return false, nil
	}
	s.mu.Lock()
	if s.proxy == nil || s.zoneHosts != nil || s.refresh == nil || s.refreshBaseKey == "" || s.buildDirs[s.refreshBaseKey] == "" || s.generation != active.generation || s.refresh.Revision == devrefresh.MaxRevision {
		s.mu.Unlock()
		return false, nil
	}
	manifest := refreshManifest(build, s.id, s.generation, s.refresh.Revision+1)
	if manifest == nil || manifest.Next(*s.refresh) != nil || !refreshArtifactsAvailable(build, manifest) {
		s.mu.Unlock()
		return false, nil
	}
	key := "refresh:" + s.generation + ":" + strconv.FormatUint(manifest.Revision, 10)
	s.pinnedBuilds = map[string]bool{s.refreshBaseKey: true, key: true}
	expired := s.retainBuildAssetsLocked(key, build.Dir, developmentAssets(build))
	s.refresh = manifest
	s.buildError = ""
	s.inspectionTransitionLocked("ready", "")
	data, _ := json.Marshal(manifest)
	s.mu.Unlock()
	s.publishRefresh(data)
	return true, expired
}

func refreshEvent(data []byte) string {
	return "event: " + devrefresh.EventName + "\ndata: " + string(data) + "\n\n"
}
func (s *supervisor) publishRefresh(data []byte) {
	message := refreshEvent(data)
	s.mu.Lock()
	defer s.mu.Unlock()
	for listener := range s.listeners {
		select {
		case listener <- message:
		default:
		}
	}
}

func (s *supervisor) serveRefresh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !s.allowedInspectionRequest(r) {
		http.Error(w, "development refresh requires a local same-origin request", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(r.URL.RawQuery) > 256 || r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		http.Error(w, "invalid refresh request", http.StatusBadRequest)
		return
	}
	// URL.Query silently discards malformed pairs; reject them explicitly.
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 2 || len(query["build"]) != 1 || len(query["generation"]) != 1 {
		http.Error(w, "invalid refresh binding", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if s.proxy == nil {
		s.mu.Unlock()
		http.Error(w, "refresh unavailable", http.StatusServiceUnavailable)
		return
	}
	if query.Get("build") != s.id || query.Get("generation") != s.generation || s.id == "" {
		s.mu.Unlock()
		http.Error(w, "development generation changed", http.StatusConflict)
		return
	}
	if s.refresh == nil {
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	data, marshalErr := json.Marshal(s.refresh)
	s.mu.Unlock()
	if marshalErr != nil || len(data) > devrefresh.MaxManifestBytes {
		http.Error(w, "refresh unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method == http.MethodGet {
		_, _ = w.Write(data)
	}
}
