//go:build !js

package app

import (
	"encoding/json"
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
	"io"
	"net/http"
	"os"
	"sync/atomic"
)

var loads atomic.Uint64

func Configure(c *nextgo.Config) {
	c.RetainRoutes = true
	c.Forbidden = func(*nextgo.Context) (nextgo.Page, error) {
		return nextgo.Page{Title: "Access denied", Body: ui.El("main", nil, ui.El("h1", nil, ui.Text("Access denied")), ui.El("a", ui.Attrs{"href": "/?q=a", "data-nextgo-prefetch": "false"}, ui.Text("Return to public demo")))}, nil
	}
	c.Handlers = map[string]http.Handler{
		"/api/health": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "pid": os.Getpid(), "pageLoads": loads.Load()})
		}),
		"/api/control": http.HandlerFunc(control),
	}
}
func cookie(c *nextgo.Context, key, fallback string) string {
	value, err := c.Request.Cookie("merge-" + key)
	if err != nil {
		return fallback
	}
	switch key {
	case "version", "partition", "schema":
		if value.Value == "1" || value.Value == "2" {
			return value.Value
		}
	case "deny":
		if value.Value == "0" || value.Value == "1" {
			return value.Value
		}
	case "navigate":
		if value.Value == "C" || value.Value == "none" {
			return value.Value
		}
	case "reject", "effectFail":
		if value.Value == "A" || value.Value == "B" || value.Value == "none" {
			return value.Value
		}
	}
	return fallback
}
func control(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST required", 405)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
		http.Error(w, "same-origin demonstration only", 403)
		return
	}
	var data struct {
		Version    string `json:"version"`
		Partition  string `json:"partition"`
		Schema     string `json:"schema"`
		Reject     string `json:"reject"`
		EffectFail string `json:"effectFail"`
		Deny       *bool  `json:"deny"`
		Navigate   string `json:"navigate"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&data) != nil || decoder.Decode(new(any)) != io.EOF || data.Version != "" && data.Version != "1" && data.Version != "2" || data.Partition != "" && data.Partition != "1" && data.Partition != "2" || data.Schema != "" && data.Schema != "1" && data.Schema != "2" || data.Reject != "" && data.Reject != "A" && data.Reject != "B" && data.Reject != "none" || data.EffectFail != "" && data.EffectFail != "A" && data.EffectFail != "B" && data.EffectFail != "none" || data.Navigate != "" && data.Navigate != "C" && data.Navigate != "none" {
		http.Error(w, "invalid public controls", 400)
		return
	}
	set := func(key, value string) {
		http.SetCookie(w, &http.Cookie{Name: "merge-" + key, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	}
	for key, value := range map[string]string{"version": data.Version, "partition": data.Partition, "schema": data.Schema, "reject": data.Reject, "effectFail": data.EffectFail, "navigate": data.Navigate} {
		if value != "" {
			set(key, value)
		}
	}
	if data.Deny != nil {
		value := "0"
		if *data.Deny {
			value = "1"
		}
		set("deny", value)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
