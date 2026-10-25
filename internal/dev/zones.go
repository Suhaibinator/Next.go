package dev

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/environment"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/project"
)

// RunZones supervises a complete multi-zone build behind one stable listener.
// The manifest and operator host override are trusted startup configuration;
// requests and forwarded headers never configure zone ownership. One explicit
// environment directory (the manifest directory) applies to the shared process.
func RunZones(ctx context.Context, manifestPath, addr string, output io.Writer) error {
	manifest, err := project.DiscoverZones(manifestPath)
	if err != nil {
		return err
	}
	ambient := developmentEnvironment(os.Environ())
	override, err := developmentZoneHostOverride(manifest.Root, ambient)
	if err != nil {
		return err
	}
	hosts, err := zoneDevelopmentHosts(manifest, override)
	if err != nil {
		return err
	}
	s := &supervisor{assets: map[string]string{}, listeners: map[chan string]bool{}, output: output, zoneHosts: hosts}
	return runSupervisor(ctx, s, addr, func(ctx context.Context) {
		s.watchBuilds(ctx, developmentBuild{
			root: manifest.Root, source: manifest.Path, watcher: &project.ZonesWatcher{},
			compile: func(ctx context.Context) (*project.Build, error) {
				return project.CompileZones(ctx, manifest.Path, true, output)
			},
			start: func(ctx context.Context, build *project.Build, publication *developmentPublication) (*child, *url.URL, error) {
				return startWithEnvironment(ctx, build, manifest.Root, output, []string{"-zone-hosts", publication.zoneHosts}, ambient)
			},
			validate: func(build *project.Build) (*developmentPublication, error) {
				override, err := developmentZoneHostOverride(manifest.Root, ambient)
				if err != nil {
					return nil, err
				}
				return zonePublication(build, override)
			},
		})
	})
}

type assetOwner struct{ host, prefix string }
type developmentPublication struct {
	hosts     map[string]bool
	owners    map[string][]assetOwner
	zoneHosts string
}

func developmentZoneHostOverride(root string, ambient []string) (string, error) {
	values, err := environment.Load(root, environment.Development, ambient)
	if err != nil {
		return "", err
	}
	for _, pair := range values {
		if value, ok := strings.CutPrefix(pair, "NEXTGO_ZONE_HOSTS="); ok {
			return value, nil
		}
	}
	return "", nil
}

func zonePublication(build *project.Build, raw string) (*developmentPublication, error) {
	if build.Zones == nil {
		return nil, fmt.Errorf("zones compiler returned no manifest metadata")
	}
	hosts, err := zoneDevelopmentHosts(build.Zones, raw)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(build.Zones.Zones))
	for i, zone := range build.Zones.Zones {
		ids[i] = zone.ID
	}
	overrides, err := nextgo.ParseZoneHosts(raw, ids)
	if err != nil {
		return nil, err
	}
	owners := make(map[string][]assetOwner)
	for _, zone := range build.Zones.Zones {
		runtime := build.ZoneRuntimeIDs[zone.ID]
		if runtime == "" || build.ZoneAssets[runtime] == "" {
			return nil, fmt.Errorf("zones compiler returned no runtime assets for zone %s", zone.ID)
		}
		host := zone.Host
		if value, ok := overrides[zone.ID]; ok {
			host = value
		}
		host, err = paths.CanonicalHost(host)
		if err != nil {
			return nil, err
		}
		owners[runtime] = append(owners[runtime], assetOwner{host: host, prefix: zone.BasePath + "/_nextgo/" + runtime + "/"})
	}
	return &developmentPublication{hosts: hosts, owners: owners, zoneHosts: raw}, nil
}

func runtimeHostMatches(owners []assetOwner, authority string) bool {
	host, err := paths.CanonicalHost(authority)
	if err != nil {
		return false
	}
	for _, owner := range owners {
		if owner.host == host {
			return true
		}
	}
	return false
}

// Caller holds s.mu. Public retained zone assets keep the exact trusted Host
// and mount of their generation. They cannot bypass another zone's ownership.
func (s *supervisor) assetRequestLocked(r *http.Request) (string, string) {
	if s.zoneHosts != nil {
		host, err := paths.CanonicalHost(r.Host)
		if err != nil {
			return "", ""
		}
		escaped := r.URL.EscapedPath()
		if _, err := (paths.Config{}).URL(escaped); err != nil {
			return "", ""
		}
		for runtime, owners := range s.assetOwners {
			for _, owner := range owners {
				if owner.host == host && strings.HasPrefix(escaped, owner.prefix) {
					relative, err := url.PathUnescape(strings.TrimPrefix(escaped, owner.prefix))
					if err != nil {
						return "", ""
					}
					return s.assets[runtime], "/_nextgo/" + runtime + "/" + relative
				}
			}
		}
		return "", ""
	}
	// Single-application development retains its existing public asset policy
	// across configured mount changes.
	assetPath := r.URL.Path
	if index := strings.Index(assetPath, "/_nextgo/"); index >= 0 {
		assetPath = assetPath[index:]
		parts := strings.Split(strings.TrimPrefix(assetPath, "/_nextgo/"), "/")
		if len(parts) > 1 {
			return s.assets[parts[0]], assetPath
		}
	}
	return "", ""
}

func zoneDevelopmentHosts(manifest *project.ZonesManifest, raw string) (map[string]bool, error) {
	if manifest == nil {
		return nil, fmt.Errorf("development requires a zones manifest")
	}
	ids := make([]string, len(manifest.Zones))
	for i, zone := range manifest.Zones {
		ids[i] = zone.ID
	}
	overrides, err := nextgo.ParseZoneHosts(raw, ids)
	if err != nil {
		return nil, err
	}
	hosts := make(map[string]bool, len(manifest.Zones))
	for _, zone := range manifest.Zones {
		host := zone.Host
		if override, ok := overrides[zone.ID]; ok {
			host = override
		}
		canonical, err := paths.CanonicalHost(host)
		if err != nil {
			return nil, fmt.Errorf("invalid development host for zone %s", zone.ID)
		}
		hosts[canonical] = true
	}
	return hosts, nil
}

// Listener controls remain reachable independently of a zone's public domain.
// Only literal startup authorities are added; arbitrary loopback aliases and
// request-provided/forwarded values do not expand a zones allowlist.
func (s *supervisor) installControlHosts(addr string) {
	s.controlHosts = make(map[string]bool)
	_, port, err := net.SplitHostPort(s.authority)
	if err != nil {
		return
	}
	add := func(value string) {
		if host, err := paths.CanonicalHost(value); err == nil {
			s.controlHosts[host] = true
		}
	}
	add(s.authority)
	if host, _, err := net.SplitHostPort(addr); err == nil && host != "" {
		add(net.JoinHostPort(host, port))
	}
}

// Caller holds s.mu. Both maps contain only canonical operator/manifest values.
func (s *supervisor) allowedZoneHostLocked(authority string) bool {
	host, err := paths.CanonicalHost(authority)
	return err == nil && (s.zoneHosts[host] || s.controlHosts[host])
}

func developmentAssets(build *project.Build) map[string]string {
	if len(build.ZoneAssets) > 0 {
		return build.ZoneAssets
	}
	return map[string]string{build.ID: filepath.Join(build.Dir, "server", "assets")}
}
