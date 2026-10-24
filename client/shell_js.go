//go:build js && wasm

package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

type shellResumeHint struct {
	page  envelope
	epoch uint64
}
type shellDecodeOptions struct {
	prefetch bool
	hint     *shellResumeHint
}

func validateShellFields(body []byte, data envelope) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}
	hasShell, hasRef := false, false
	for key := range fields {
		if strings.EqualFold(key, "shell") {
			if key != "shell" {
				return fmt.Errorf("invalid shell field spelling")
			}
			hasShell = true
		}
		if strings.EqualFold(key, "shellRef") {
			if key != "shellRef" {
				return fmt.Errorf("invalid shell reference field spelling")
			}
			hasRef = true
		}
	}
	if !hasShell && !hasRef {
		return nil
	}
	if hasRef && !hasShell {
		return fmt.Errorf("shell reference flag has no generation")
	}
	if wirejson.Unique(body) != nil {
		return fmt.Errorf("invalid shell envelope JSON")
	}
	if hasShell {
		raw := bytes.TrimSpace(fields["shell"])
		if len(raw) < 2 || raw[0] != '"' || !navigation.ValidShellID(data.Shell) {
			return fmt.Errorf("invalid shell generation")
		}
	}
	if hasRef {
		raw := bytes.TrimSpace(fields["shellRef"])
		if !bytes.Equal(raw, []byte("true")) && !bytes.Equal(raw, []byte("false")) {
			return fmt.Errorf("invalid shell reference flag")
		}
	}
	if data.ShellRef && (!hasShell || data.HTML != "") {
		return fmt.Errorf("invalid shell reference payload")
	}
	if data.ShellRef {
		if raw, ok := fields["html"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte(`""`)) {
			return fmt.Errorf("invalid referenced shell HTML")
		}
	}
	return nil
}

func applyShellReference(data *envelope, hint *shellResumeHint, target string) error {
	if hint == nil || hint.epoch != cacheEpoch || !navigation.ValidShellID(data.Shell) || data.Shell != hint.page.Shell || hint.page.ShellRef || hint.page.HTML == "" || data.Status != 200 || hint.page.Status != 200 {
		return fmt.Errorf("shell reference has no current cached generation")
	}
	saved := hint.page
	expected := requestURL(win.Get("URL").New(saved.URL, target).Get("href").String())
	actual := requestURL(win.Get("URL").New(data.URL, target).Get("href").String())
	if actual != expected || data.Version != saved.Version || data.Build != saved.Build || data.Configuration != saved.Configuration || data.Title != saved.Title || data.Description != saved.Description || data.Document != saved.Document || !reflect.DeepEqual(data.Meta, saved.Meta) || !reflect.DeepEqual(data.Head, saved.Head) {
		return fmt.Errorf("shell reference metadata does not match cached generation")
	}
	data.HTML = saved.HTML
	data.shellEpoch = hint.epoch
	return nil
}

func discardShellHint(key string) {
	pageCache.mu.Lock()
	defer pageCache.mu.Unlock()
	pageCache.remove(key)
}
