package nextgo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

type zoneContextKey struct{}

func ownZoneInfo(info ZoneInfo) ZoneInfo {
	return ZoneInfo{ID: strings.Clone(info.ID), Tenant: strings.Clone(info.Tenant), Host: strings.Clone(info.Host), BasePath: strings.Clone(info.BasePath)}
}
func validZoneScopeText(value string, limit int, required bool) bool {
	return (!required || value != "") && len(value) <= limit && utf8.ValidString(value) && strings.TrimSpace(value) == value && strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) }) < 0
}

// Zone returns an owned snapshot of trusted deployment metadata installed by
// NewZones before Configure. It is zero outside a configured zone. It identifies
// a deployment selection, not a signed-in user, authorization or data filter.
func (c Config) Zone() ZoneInfo {
	if c.zoneInfo == nil {
		return ZoneInfo{}
	}
	return ownZoneInfo(*c.zoneInfo)
}
func setConfigZone(c *Config, info ZoneInfo) error {
	if c == nil || !validZoneScopeText(info.ID, 128, true) || !validZoneScopeText(info.Tenant, 256, false) {
		return fmt.Errorf("nextgo: invalid trusted zone metadata")
	}
	host, err := paths.CanonicalHost(info.Host)
	if err != nil {
		return fmt.Errorf("nextgo: invalid trusted zone host")
	}
	if err := (paths.Config{BasePath: info.BasePath}).Validate(); err != nil {
		return err
	}
	if c.BasePath != info.BasePath {
		return fmt.Errorf("nextgo: zone base path differs from configured mount")
	}
	info.Host = host
	owned := ownZoneInfo(info)
	c.zoneInfo = &owned
	return nil
}

// CurrentZone returns trusted immutable deployment-selection metadata. Request
// headers, URL parameters and cookies never establish this value. Callers must
// still authorize application users and select/filter their backing data.
func CurrentZone(ctx context.Context) ZoneInfo {
	if ctx == nil {
		return ZoneInfo{}
	}
	info, _ := ctx.Value(zoneContextKey{}).(ZoneInfo)
	return ownZoneInfo(info)
}
func withZoneContext(ctx context.Context, info ZoneInfo) context.Context {
	return context.WithValue(ctx, zoneContextKey{}, ownZoneInfo(info))
}

// Override every existing marker, including the zero value. An independent App
// must not inherit another App's zone from a reused startup/request context.
func bindConfigZone(ctx context.Context, c Config) context.Context {
	if c.zoneInfo == nil && ctx.Value(zoneContextKey{}) == nil {
		return ctx
	}
	return withZoneContext(ctx, c.Zone())
}
func (a *App) bindZoneContext(ctx context.Context) context.Context {
	return bindConfigPublicOrigin(bindConfigZone(ctx, a.config), a.config)
}

// Compose the existing opaque cache/configuration identity. Zone fields remain
// native deployment authority; no public manifest/schema field is introduced.
func configurationZonePolicy(previous string, info ZoneInfo) string {
	if info.ID == "" {
		return previous
	}
	sum := sha256.New()
	_, _ = sum.Write([]byte("nextgo.zone.configuration.v1\x00"))
	for _, value := range []string{previous, info.ID, info.Tenant, info.Host, info.BasePath} {
		_, _ = fmt.Fprintf(sum, "%d:%s", len(value), value)
	}
	return hex.EncodeToString(sum.Sum(nil))
}
