package actions

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

func TestClonePreservesPoliciesAndSharedHooksWithIndependentRegistrations(t *testing.T) {
	var calls, revalidated, logged atomic.Int64
	origins := []string{"https://allowed.test"}
	original := New(Options{AllowedOrigins: origins, MaxBodyBytes: 8, UnauthorizedChallenge: `Bearer realm="clone"`, OnRevalidate: func(tags, paths []string) {
		if len(tags) != 1 || len(paths) != 1 {
			t.Error("revalidation metadata lost")
		}
		revalidated.Add(1)
	}, LogError: func(*http.Request, error) { logged.Add(1) }})
	original.HandleContract("save/v1", "v1", func(*Context) (Result, error) {
		calls.Add(1)
		return Result{Data: "ok", RevalidateTags: []string{"tag"}, RevalidatePaths: []string{"/page"}}, nil
	})
	original.Handle("denied", func(*Context) (Result, error) { return Result{}, ErrUnauthorized })
	original.Handle("failed", func(*Context) (Result, error) { return Result{}, fmt.Errorf("private failure") })
	clone := original.Clone()
	origins[0] = "https://changed.test"
	clone.options.AllowedOrigins[0] = "https://clone.test"
	clone.origins["https://clone.test"] = true
	clone.Handle("clone-only", func(*Context) (Result, error) { return Result{Data: "clone"}, nil })
	original.Handle("original-only", func(*Context) (Result, error) { return Result{Data: "original"}, nil })
	for _, router := range []*Router{original, clone} {
		req := request("POST", "/save/v1", `{}`, "application/json")
		req.Header.Set("Origin", "https://allowed.test")
		response := perform(router, req)
		if response.Code != 200 || response.Header().Get("X-Nextgo-Action-Revision") != "v1" {
			t.Fatal("clone lost allowedorigin/contract", response.Code, response.Body.String())
		}
		denied := perform(router, request("POST", "/denied", `{}`, "application/json"))
		if denied.Code != 401 || denied.Header().Get("WWW-Authenticate") != `Bearer realm="clone"` {
			t.Fatal("clone lost authorization policy", denied.Code, denied.Header())
		}
		large := perform(router, request("POST", "/save/v1", `{"large":true}`, "application/json"))
		if large.Code != 413 {
			t.Fatal("clone lost body limit", large.Code)
		}
		failed := perform(router, request("POST", "/failed", `{}`, "application/json"))
		if failed.Code != 500 || strings.Contains(failed.Body.String(), "private failure") {
			t.Fatal("clone lost failure sanitization")
		}
	}
	if calls.Load() != 2 || revalidated.Load() != 2 || logged.Load() != 2 {
		t.Fatal("handler/hook functions not preserved", calls.Load(), revalidated.Load(), logged.Load())
	}
	if perform(original, request("POST", "/clone-only", `{}`, "application/json")).Code != 404 || perform(clone, request("POST", "/original-only", `{}`, "application/json")).Code != 404 {
		t.Fatal("registration maps shared")
	}
	rejected := request("POST", "/save/v1", `{}`, "application/json")
	rejected.Header.Set("Origin", "https://clone.test")
	if perform(original, rejected).Code != 403 {
		t.Fatal("origin maps shared")
	}
	if original.options.AllowedOrigins[0] != "https://allowed.test" {
		t.Fatal("constructor/clone retained caller slice")
	}
}

func TestCloneOwnsMetadataBackingStrings(t *testing.T) {
	backing := strings.Repeat("x", 2<<20) + "save/v1" + "v1" + "https://allowed.test" + `Bearer realm="clone"`
	offset := 2 << 20
	name := backing[offset : offset+7]
	offset += 7
	revision := backing[offset : offset+2]
	offset += 2
	origin := backing[offset : offset+20]
	offset += 20
	challenge := backing[offset:]
	original := New(Options{AllowedOrigins: []string{origin}, UnauthorizedChallenge: challenge})
	original.HandleContract(name, revision, func(*Context) (Result, error) { return Result{}, nil })
	clone := original.Clone()
	start := uintptr(unsafe.Pointer(unsafe.StringData(backing)))
	end := start + uintptr(len(backing))
	owned := func(label, value string) {
		t.Helper()
		if value == "" {
			return
		}
		pointer := uintptr(unsafe.Pointer(unsafe.StringData(value)))
		if pointer >= start && pointer < end {
			t.Fatal(label, "retains large caller backing")
		}
	}
	owned("constructor origin", original.options.AllowedOrigins[0])
	owned("constructor challenge", original.options.UnauthorizedChallenge)
	owned("clone origin", clone.options.AllowedOrigins[0])
	owned("clone challenge", clone.options.UnauthorizedChallenge)
	for key, entry := range clone.handlers {
		owned("registration name", key)
		owned("registration revision", entry.revision)
	}
	for key := range clone.origins {
		owned("normalized origin", key)
	}
	runtime.KeepAlive(backing)
}

func TestCloneConcurrentRegistrationSnapshotAndNil(t *testing.T) {
	var missing *Router
	if missing.Clone() != nil {
		t.Fatal("nil clone should remain nil")
	}
	original := New(Options{})
	original.Handle("base", func(*Context) (Result, error) { return Result{}, nil })
	var registrations sync.WaitGroup
	registrations.Add(1)
	go func() {
		defer registrations.Done()
		for i := 0; i < 100; i++ {
			original.Handle(fmt.Sprint("added/", i), func(*Context) (Result, error) { return Result{}, nil })
		}
	}()
	for i := 0; i < 100; i++ {
		clone := original.Clone()
		if clone.handlers["base"].handler == nil {
			t.Fatal("incomplete registration snapshot")
		}
		clone.Handle("clone-only", func(*Context) (Result, error) { return Result{}, nil })
	}
	registrations.Wait()
	if original.handlers["clone-only"].handler != nil || len(original.handlers) != 101 {
		t.Fatal("clone modified original registry")
	}
}
