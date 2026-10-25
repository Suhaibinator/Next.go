//go:build !js

package work

import (
	"net/http"
	"net/url"
	"strconv"
	"time"
)

func Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(405)
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 1 || len(q["delay"]) != 1 {
		http.Error(w, "Bad Request", 400)
		return
	}
	delay, err := strconv.Atoi(q.Get("delay"))
	if err != nil || delay < 0 || delay > 15000 {
		http.Error(w, "Bad Request", 400)
		return
	}
	timer := time.NewTimer(time.Duration(delay) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-r.Context().Done():
		return
	}
	if r.Context().Err() != nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "GET" {
		_, _ = w.Write([]byte("{\"complete\":true}\n"))
	}
}
