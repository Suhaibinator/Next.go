//go:build !js

// Package identity is an explicit synthetic identity issuer for public fixtures.
// Anyone can select Alice or Bob: it is not real account authentication. A fresh
// process key signs demo sessions so routing headers cannot mint a subject.
package identity

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
)

const CookieName = "nextgo-zones-demo"

var signingKey = func() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return key
}()

func Issue(subject string) (*http.Cookie, error) {
	if subject != "alice" && subject != "bob" {
		return nil, nextgo.ErrUnauthorized
	}
	expires := time.Now().Add(30 * time.Minute)
	payload := subject + ":" + strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, signingKey)
	_, _ = mac.Write([]byte("zones-demo-v1:" + payload))
	value := base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return &http.Cookie{Name: CookieName, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: 1800}, nil
}

func Subject(r *http.Request) (string, error) {
	if r == nil {
		return "", nextgo.ErrUnauthorized
	}
	var value string
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == CookieName {
			value = cookie.Value
			count++
		}
	}
	if count != 1 || len(value) > 256 {
		return "", nextgo.ErrUnauthorized
	}
	payloadText, signature, ok := strings.Cut(value, ".")
	if !ok {
		return "", nextgo.ErrUnauthorized
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadText)
	if err != nil {
		return "", nextgo.ErrUnauthorized
	}
	actual, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return "", nextgo.ErrUnauthorized
	}
	mac := hmac.New(sha256.New, signingKey)
	_, _ = mac.Write([]byte("zones-demo-v1:" + string(payload)))
	if !hmac.Equal(actual, mac.Sum(nil)) {
		return "", nextgo.ErrUnauthorized
	}
	subject, deadline, ok := strings.Cut(string(payload), ":")
	expires, err := strconv.ParseInt(deadline, 10, 64)
	if !ok || err != nil || expires <= time.Now().Unix() || subject != "alice" && subject != "bob" {
		return "", nextgo.ErrUnauthorized
	}
	return subject, nil
}

// Member is a fresh server-side check independent of zone/host selection.
func Member(subject, tenant string) bool {
	return subject == "alice" && tenant == "alpha" || subject == "bob" && tenant == "beta"
}
