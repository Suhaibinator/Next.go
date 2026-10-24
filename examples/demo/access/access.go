//go:build !js

// Package access is an illustrative server-only opaque-token policy. A real
// application supplies its identity/session provider and data authorization.
package access

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"os"
	"strings"

	nextgo "github.com/Suhaibinator/Next.go"
	_ "github.com/Suhaibinator/Next.go/serveronly"
)

type Role string

const (
	Reader        Role = "reader"
	Administrator Role = "administrator"
)

func validToken(value string) bool {
	return len(value) >= 32 && len(value) <= 512 && !strings.ContainsAny(value, " \t\r\n")
}

// Authenticate accepts one explicit Bearer credential. Query strings and
// caller-supplied role headers never establish identity. Empty/unconfigured
// accounts are disabled; no credential or report is serialized to the browser.
func Authenticate(r *http.Request) (Role, error) {
	if r == nil || len(r.Header.Values("Authorization")) != 1 {
		return "", nextgo.ErrUnauthorized
	}
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || !validToken(token) {
		return "", nextgo.ErrUnauthorized
	}
	digest := sha256.Sum256([]byte(token))
	reader, admin := os.Getenv("NEXTGO_DEMO_READER_TOKEN"), os.Getenv("NEXTGO_DEMO_ADMIN_TOKEN")
	readerDigest, adminDigest := sha256.Sum256([]byte(reader)), sha256.Sum256([]byte(admin))
	readerMatch, adminMatch := subtle.ConstantTimeCompare(digest[:], readerDigest[:]), subtle.ConstantTimeCompare(digest[:], adminDigest[:])
	if adminMatch == 1 && validToken(admin) {
		return Administrator, nil
	}
	if readerMatch == 1 && validToken(reader) {
		return Reader, nil
	}
	return "", nextgo.ErrUnauthorized
}

type Report struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// ReadReport authenticates and authorizes at the data boundary before loading
// the record. Reuse this check in every API/action that reads or mutates it.
func ReadReport(ctx context.Context, r *http.Request) (Report, error) {
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	role, err := Authenticate(r)
	if err != nil {
		return Report{}, err
	}
	if role != Administrator {
		return Report{}, nextgo.ErrForbidden
	}
	// Replace this illustrative value with a request-context-aware repository call.
	return Report{Title: "Private deployment report", Summary: "Only an authorized server request can load this report."}, nil
}
