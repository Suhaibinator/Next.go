//go:build !js

package app

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
)

var pageLoads atomic.Uint64
var taskStarted, taskCanceled, taskActive atomic.Uint64
var taskSlots = make(chan struct{}, 4)

func Configure(config *nextgo.Config) {
	config.RetainRoutes = true
	config.Handlers = map[string]http.Handler{
		"/api/health": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "pid": os.Getpid(), "pageLoads": pageLoads.Load(), "tasksStarted": taskStarted.Load(), "tasksCanceled": taskCanceled.Load(), "tasksActive": taskActive.Load()})
		}),
		"/api/control": http.HandlerFunc(control),
		"/api/task":    http.HandlerFunc(ownedTask),
	}
}

// These controls alter only this browser's public demonstration cookies. They
// do not authenticate users or grant access to private data.
func control(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
		http.Error(w, "same-origin demonstration only", http.StatusForbidden)
		return
	}
	var value struct {
		Partition string `json:"partition"`
		Version   string `json:"version"`
		Deny      *bool  `json:"deny"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF || value.Partition != "" && value.Partition != "1" && value.Partition != "2" || value.Version != "" && value.Version != "1" && value.Version != "2" {
		http.Error(w, "invalid demonstration control", http.StatusBadRequest)
		return
	}
	set := func(name, text string) {
		http.SetCookie(w, &http.Cookie{Name: name, Value: text, Path: "/", SameSite: http.SameSiteLaxMode, HttpOnly: true})
	}
	if value.Partition != "" {
		set("retained-partition", value.Partition)
	}
	if value.Version != "" {
		set("retained-version", value.Version)
	}
	if value.Deny != nil {
		if *value.Deny {
			set("retained-deny", "1")
		} else {
			set("retained-deny", "0")
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func demoCookie(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err == nil && cookie.Value == "2" {
		return "2"
	}
	return "1"
}

func demoPartition(r *http.Request) string {
	return "public-demo-" + demoCookie(r, "retained-partition")
}

func demoDenied(r *http.Request) bool {
	cookie, err := r.Cookie("retained-deny")
	return err == nil && strings.TrimSpace(cookie.Value) == "1"
}

// A small real HTTP read demonstrates cancellation. It is not a durable job.
func ownedTask(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	select {
	case taskSlots <- struct{}{}:
	default:
		http.Error(w, "demonstration task capacity reached", http.StatusTooManyRequests)
		return
	}
	defer func() { <-taskSlots }()
	taskStarted.Add(1)
	taskActive.Add(1)
	defer taskActive.Add(^uint64(0))
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	select {
	case <-r.Context().Done():
		taskCanceled.Add(1)
		return
	case <-timer.C:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("completed"))
	}
}
