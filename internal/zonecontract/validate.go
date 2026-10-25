package zonecontract

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/actionwire"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/metadatafiles"
)

func validIdentifier(value string, limit int) bool {
	if value == "" || len(value) > limit {
		return false
	}
	for i, c := range []byte(value) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || i > 0 && strings.ContainsRune("._-", rune(c))) {
			return false
		}
	}
	return true
}

func validHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, c := range []byte(value) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func NormalizeBinding(value Binding) (Binding, error) {
	if !validIdentifier(value.ID, 64) {
		return Binding{}, fmt.Errorf("zone contract: invalid bounded zone ID")
	}
	host, err := paths.CanonicalHost(value.Host)
	if err != nil {
		return Binding{}, fmt.Errorf("zone contract: invalid exact public host")
	}
	if err := (paths.Config{BasePath: value.BasePath}).Validate(); err != nil {
		return Binding{}, fmt.Errorf("zone contract: invalid public mount: %w", err)
	}
	value = value.Clone()
	value.Host = strings.Clone(host)
	return value, nil
}

// CanonicalOrigin accepts only an HTTP(S) origin, without even a trailing path
// slash. It preserves an explicitly configured port and never reads requests,
// forwarding headers, environment variables, or DNS.
func CanonicalOrigin(value string) (string, error) {
	if len(value) == 0 || len(value) > 8192 || strings.ContainsAny(value, "\\\x00\r\n\t ") {
		return "", fmt.Errorf("zone contract: invalid HTTP(S) origin")
	}
	u, err := url.Parse(value)
	if err != nil || u == nil || u.User != nil || u.Opaque != "" || u.Host == "" || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || strings.ContainsAny(value, "?#") {
		return "", fmt.Errorf("zone contract: origin requires only HTTP(S) scheme and authority")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("zone contract: origin requires HTTP(S)")
	}
	host, err := paths.CanonicalHost(u.Host)
	if err != nil {
		return "", fmt.Errorf("zone contract: invalid exact origin authority")
	}
	return scheme + "://" + host, nil
}

func ReleasePrefix(base, releaseID string) (string, error) {
	if err := (paths.Config{BasePath: base}).Validate(); err != nil {
		return "", fmt.Errorf("zone contract: invalid release mount: %w", err)
	}
	if !validIdentifier(releaseID, 64) {
		return "", fmt.Errorf("zone contract: invalid bounded release ID")
	}
	return strings.Clone(base) + "/_nextgo/releases/" + strings.Clone(releaseID), nil
}

func normalizeDocumentPaths(values []string) ([]string, error) {
	if len(values) > MaxDocumentPaths {
		return nil, fmt.Errorf("zone contract: document path count exceeds %d", MaxDocumentPaths)
	}
	total := 0
	for _, value := range values {
		total += len(value)
		if total > MaxDocumentBytes || value == "" {
			return nil, fmt.Errorf("zone contract: invalid document path byte budget or root")
		}
		if err := (paths.Config{BasePath: value}).Validate(); err != nil {
			return nil, fmt.Errorf("zone contract: invalid literal document path: %w", err)
		}
	}
	if len(values) == 0 {
		return nil, nil
	}
	owned := make([]string, len(values))
	for i, value := range values {
		owned[i] = strings.Clone(value)
	}
	slices.Sort(owned)
	return slices.Compact(owned), nil
}

func NormalizeReleaseSpec(value ReleaseSpec) (ReleaseSpec, error) {
	if value.Version != Version || value.NavigationProtocol != navigation.ProtocolVersion || value.ActionProtocol != actionwire.Protocol {
		return ReleaseSpec{}, fmt.Errorf("zone contract: unsupported release or framework protocol version")
	}
	binding, err := NormalizeBinding(value.Zone)
	if err != nil {
		return ReleaseSpec{}, err
	}
	prefix, err := ReleasePrefix(binding.BasePath, value.ReleaseID)
	if err != nil {
		return ReleaseSpec{}, err
	}
	if value.AssetPrefix != prefix {
		return ReleaseSpec{}, fmt.Errorf("zone contract: asset prefix must equal the declared release prefix")
	}
	if !validHex(value.RuntimeID, 20) || !validHex(value.AssetsSHA256, 64) || !validHex(value.TopologySHA256, 64) {
		return ReleaseSpec{}, fmt.Errorf("zone contract: runtime ID or mandatory SHA-256 identity is invalid")
	}
	origin, err := CanonicalOrigin(value.PublicOrigin)
	if err != nil {
		return ReleaseSpec{}, err
	}
	u, _ := url.Parse(origin)
	if u.Host != binding.Host {
		return ReleaseSpec{}, fmt.Errorf("zone contract: public origin differs from public zone host")
	}
	documents, err := normalizeDocumentPaths(value.DocumentPaths)
	if err != nil {
		return ReleaseSpec{}, err
	}
	value = value.Clone()
	value.Zone, value.PublicOrigin, value.DocumentPaths = binding, origin, documents
	return value, nil
}

func NormalizeReleaseManifest(value ReleaseManifest) (ReleaseManifest, error) {
	if !validHex(value.BinarySHA256, 64) {
		return ReleaseManifest{}, fmt.Errorf("zone contract: invalid mandatory binary SHA-256")
	}
	release, err := NormalizeReleaseSpec(value.Release)
	if err != nil {
		return ReleaseManifest{}, err
	}
	return ReleaseManifest{Release: release, BinarySHA256: strings.Clone(value.BinarySHA256)}, nil
}

func normalizeBindings(input []Binding) ([]Binding, error) {
	if len(input) == 0 || len(input) > MaxZones {
		return nil, fmt.Errorf("zone contract: require 1 to %d zones", MaxZones)
	}
	values := make([]Binding, len(input))
	ids, mounts := map[string]bool{}, map[string]bool{}
	for i, value := range input {
		normalized, err := NormalizeBinding(value)
		if err != nil {
			return nil, err
		}
		key, mount := strings.ToLower(normalized.ID), normalized.Host+"\x00"+normalized.BasePath
		if ids[key] || mounts[mount] {
			return nil, fmt.Errorf("zone contract: duplicate/case-colliding zone ID or public binding")
		}
		ids[key], mounts[mount], values[i] = true, true, normalized
	}
	for i, parent := range values {
		for j, child := range values {
			if i == j || parent.Host != child.Host {
				continue
			}
			logical, inside := paths.Strip(parent.BasePath, child.BasePath)
			if !inside {
				continue
			}
			first, _, _ := strings.Cut(strings.TrimPrefix(logical, "/"), "/")
			if first == "api" || first == "assets" || first == "_nextgo" || metadatafiles.IsName(first) {
				return nil, fmt.Errorf("zone contract: nested zone enters reserved parent endpoint ownership")
			}
		}
	}
	return values, nil
}

func NormalizeIngressManifest(value IngressManifest) (IngressManifest, error) {
	if value.Version != Version || !validHex(value.TopologySHA256, 64) {
		return IngressManifest{}, fmt.Errorf("zone contract: invalid ingress version or topology digest")
	}
	if len(value.Zones) == 0 || len(value.Zones) > MaxZones {
		return IngressManifest{}, fmt.Errorf("zone contract: require 1 to %d ingress zones", MaxZones)
	}
	bindings := make([]Binding, len(value.Zones))
	for i, zone := range value.Zones {
		bindings[i] = zone.Binding
	}
	bindings, err := normalizeBindings(bindings)
	if err != nil {
		return IngressManifest{}, err
	}
	owned := IngressManifest{Version: Version, TopologySHA256: strings.Clone(value.TopologySHA256), Zones: make([]IngressZone, len(value.Zones))}
	total := 0
	artifactBytes := 0
	publicOrigins := map[string]string{}
	for i, zone := range value.Zones {
		if !validIdentifier(zone.ActiveRelease, 64) || len(zone.Releases) == 0 || len(zone.Releases) > MaxReleasesPerZone {
			return IngressManifest{}, fmt.Errorf("zone contract: invalid active release or release count")
		}
		total += len(zone.Releases)
		if total > MaxReleases {
			return IngressManifest{}, fmt.Errorf("zone contract: release inventory exceeds %d", MaxReleases)
		}
		result := IngressZone{Binding: bindings[i], ActiveRelease: strings.Clone(zone.ActiveRelease), Releases: make([]IngressRelease, len(zone.Releases))}
		seen, active := map[string]bool{}, false
		required, err := NestedDocumentPaths(bindings[i], bindings)
		if err != nil {
			return IngressManifest{}, err
		}
		for j, release := range zone.Releases {
			artifact, err := NormalizeReleaseManifest(release.Artifact)
			if err != nil {
				return IngressManifest{}, err
			}
			encoded, err := json.Marshal(artifact)
			if err != nil {
				return IngressManifest{}, err
			}
			artifactBytes += len(encoded)
			if artifactBytes > MaxManifestBytes {
				return IngressManifest{}, fmt.Errorf("zone contract: release inventory exceeds manifest byte limit")
			}
			spec := artifact.Release
			if spec.Zone != bindings[i] || spec.TopologySHA256 != owned.TopologySHA256 {
				return IngressManifest{}, fmt.Errorf("zone contract: release differs from ingress zone or topology")
			}
			key := strings.ToLower(spec.ReleaseID)
			if seen[key] {
				return IngressManifest{}, fmt.Errorf("zone contract: duplicate/case-colliding release ID")
			}
			seen[key] = true
			active = active || spec.ReleaseID == zone.ActiveRelease
			for _, path := range required {
				if !slices.Contains(spec.DocumentPaths, path) {
					return IngressManifest{}, fmt.Errorf("zone contract: release omits a nested zone document boundary")
				}
			}
			if previous := publicOrigins[spec.Zone.Host]; previous != "" && previous != spec.PublicOrigin {
				return IngressManifest{}, fmt.Errorf("zone contract: one public host cannot declare different origin schemes")
			}
			publicOrigins[spec.Zone.Host] = spec.PublicOrigin
			origin, err := CanonicalOrigin(release.Origin)
			if err != nil {
				return IngressManifest{}, err
			}
			result.Releases[j] = IngressRelease{Artifact: artifact, Origin: origin}
		}
		if !active {
			return IngressManifest{}, fmt.Errorf("zone contract: active release is absent from retained inventory")
		}
		slices.SortFunc(result.Releases, func(a, b IngressRelease) int {
			return strings.Compare(a.Artifact.Release.ReleaseID, b.Artifact.Release.ReleaseID)
		})
		owned.Zones[i] = result
	}
	slices.SortFunc(owned.Zones, func(a, b IngressZone) int { return strings.Compare(a.Binding.ID, b.Binding.ID) })
	encoded, err := json.Marshal(owned)
	if err != nil {
		return IngressManifest{}, err
	}
	if len(encoded) > MaxManifestBytes {
		return IngressManifest{}, fmt.Errorf("zone contract: ingress exceeds manifest byte limit")
	}
	return owned, nil
}
