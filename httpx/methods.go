package httpx

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Methods snapshots an HTTP method table. GET also handles HEAD unless HEAD is
// explicit; automatic OPTIONS returns the Allow header. An explicit OPTIONS
// handler takes precedence. Authentication/CORS remain application policy.
func Methods(handlers map[string]http.Handler) (http.Handler, error) {
	if len(handlers) == 0 {
		return nil, fmt.Errorf("httpx: empty method table")
	}
	table := make(map[string]http.Handler, len(handlers)+1)
	for method, handler := range handlers {
		if method == "" || strings.ToUpper(method) != method || handler == nil {
			return nil, fmt.Errorf("httpx: invalid method or nil handler %q", method)
		}
		for _, c := range method {
			if !strings.ContainsRune("!#$%&'*+-.^_`|~0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ", c) {
				return nil, fmt.Errorf("httpx: invalid method %q", method)
			}
		}
		table[method] = handler
	}
	if table["GET"] != nil && table["HEAD"] == nil {
		table["HEAD"] = table["GET"]
	}
	allowed := make([]string, 0, len(table)+1)
	for method := range table {
		allowed = append(allowed, method)
	}
	if table["OPTIONS"] == nil {
		allowed = append(allowed, "OPTIONS")
	}
	sort.Strings(allowed)
	allow := strings.Join(allowed, ", ")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handler := table[r.Method]; handler != nil {
			handler.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Allow", allow)
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}), nil
}
