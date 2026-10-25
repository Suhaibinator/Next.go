//go:build !js

// Package tenantdata selects only synthetic public records using trusted zone
// context, then independently enforces membership of the issued demo subject.
package tenantdata

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/examples/zones/identity"
)

type Record struct {
	Tenant   string `json:"tenant"`
	Title    string `json:"title"`
	Subject  string `json:"subject"`
	Revision int64  `json:"revision"`
}
type stateKey struct{}
type state struct{ revision atomic.Int64 }

func Authorize(ctx context.Context, r *http.Request) (string, string, error) {
	info := nextgo.CurrentZone(ctx)
	subject, err := identity.Subject(r)
	if err != nil {
		return "", "", err
	}
	if !identity.Member(subject, info.Tenant) {
		return "", "", nextgo.ErrForbidden
	}
	return subject, info.Tenant, nil
}
func Read(ctx context.Context, r *http.Request) (Record, error) {
	subject, tenant, err := Authorize(ctx, r)
	if err != nil {
		return Record{}, err
	}
	revision := int64(0)
	if s, _ := ctx.Value(stateKey{}).(*state); s != nil {
		revision = s.revision.Load()
	}
	return Record{Tenant: tenant, Title: tenant + " synthetic field notes", Subject: subject, Revision: revision}, nil
}

func Configure(c *nextgo.Config) {
	c.PreserveLayouts = true
	s := &state{}
	c.Middleware = append(c.Middleware, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), stateKey{}, s)))
		})
	})
	router := actions.New(actions.Options{MaxBodyBytes: 2048})
	router.Handle("assume", func(ctx *actions.Context) (actions.Result, error) {
		values, err := ctx.ParseForm()
		if err != nil {
			return actions.Result{}, err
		}
		if len(values) != 1 || len(values["subject"]) != 1 {
			return actions.Result{}, &actions.ValidationError{Message: "Select one synthetic subject."}
		}
		cookie, err := identity.Issue(values.Get("subject"))
		if err != nil {
			return actions.Result{}, err
		}
		return actions.Result{Data: map[string]string{"subject": values.Get("subject"), "issuer": "public synthetic demo"}, Redirect: "/dashboard", Cookies: []*http.Cookie{cookie}}, nil
	})
	router.Handle("preview", func(ctx *actions.Context) (actions.Result, error) {
		subject, tenant, err := Authorize(ctx, ctx.Request)
		if err != nil {
			return actions.Result{}, err
		}
		values, err := ctx.ParseForm()
		if err != nil {
			return actions.Result{}, err
		}
		if len(values) != 1 || len(values["note"]) != 1 || len(values.Get("note")) > 80 {
			return actions.Result{}, &actions.ValidationError{Message: "Use one public note of at most 80 bytes."}
		}
		revision := s.revision.Add(1)
		return actions.Result{Data: map[string]any{"tenant": tenant, "subject": subject, "zone": nextgo.CurrentZone(ctx).ID, "revision": revision}, Redirect: "/dashboard?preview=1"}, nil
	})
	c.Actions = router
}

func API(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(405)
		return
	}
	record, err := Read(r.Context(), r)
	if err != nil {
		status := 403
		if err == nextgo.ErrUnauthorized {
			status = 401
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "GET" {
		_ = json.NewEncoder(w).Encode(record)
	}
}
