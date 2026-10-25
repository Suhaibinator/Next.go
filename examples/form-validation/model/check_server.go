//go:build !js

package model

import (
	"context"
	"errors"
	"strings"
	"time"
)

var ErrUnavailable = errors.New("public validation check unavailable")

func PreviewFields(ctx context.Context, value Values) (map[string]string, error) {
	fields := Validate(value)
	for _, field := range []string{"name", "code"} {
		text := value.Name
		if field == "code" {
			text = value.Code
		}
		message, err := Check(ctx, field, text)
		if err != nil {
			return nil, err
		}
		if message != "" {
			fields[field] = message
		}
	}
	return fields, ctx.Err()
}

// Check is a deterministic public demonstration rule, not an availability
// database. Its bounded delay cooperates with canceled validation requests.
func Check(ctx context.Context, field, value string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	text := strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(text, "slow") {
		timer := time.NewTimer(800 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if text == "offline" {
		return "", ErrUnavailable
	}
	if text == "reserved" || text == "slow-reserved" {
		if field == "name" {
			return "This public name is reserved.", nil
		}
		return "This public code is reserved.", nil
	}
	return "", ctx.Err()
}
