//go:build !js

package profiles

import (
	"context"
	"fmt"
	_ "github.com/Suhaibinator/Next.go/serveronly"
	"strconv"
	"strings"
	"sync"
)

var store = struct {
	sync.Mutex
	records map[string]Profile
}{records: map[string]Profile{}}

func Get(ctx context.Context, id string) (Profile, error) {
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	if !ValidID(id) {
		return Profile{}, fmt.Errorf("invalid demo record ID")
	}
	store.Lock()
	defer store.Unlock()
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	if p, ok := store.records[id]; ok {
		return p, nil
	}
	return Profile{ID: id, Version: "0", Name: "Ada Lovelace", Bio: "Notes about this profile.", Email: "ada@example.test", Role: "reader"}, nil
}

// Save is deliberately public, process-local demo data. It is not an identity,
// authorization or durable storage implementation.
func Save(ctx context.Context, p Profile) (Profile, map[string]string, error) {
	if fields := Validate(p); len(fields) > 0 {
		return Profile{}, fields, nil
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(p.Email)), "blocked@") {
		return Profile{}, map[string]string{"email": "This demo address is reserved."}, nil
	}
	store.Lock()
	defer store.Unlock()
	if err := ctx.Err(); err != nil {
		return Profile{}, nil, err
	}
	current, exists := store.records[p.ID]
	if !exists {
		current.Version = "0"
	}
	if p.Version != current.Version {
		return Profile{}, map[string]string{"version": "This profile changed. Reload before saving."}, nil
	}
	if !exists && len(store.records) >= 256 {
		return Profile{}, nil, fmt.Errorf("demo profile capacity reached")
	}
	version, _ := strconv.ParseUint(current.Version, 10, 64)
	p.Version = strconv.FormatUint(version+1, 10)
	p.Name = strings.TrimSpace(p.Name)
	p.Bio = strings.ReplaceAll(strings.ReplaceAll(p.Bio, "\r\n", "\n"), "\r", "\n")
	p.Email = strings.ToLower(strings.TrimSpace(p.Email))
	store.records[p.ID] = p
	return p, nil, nil
}
