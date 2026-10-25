// Package zonecontract defines the bounded, native deployment contract shared
// by independently built zone endpoints, their compiler, and trusted ingress.
// Manifests and identity headers are compatibility metadata, not authentication.
package zonecontract

import "strings"

const (
	Version            = 1
	MaxManifestBytes   = 1 << 20
	MaxZones           = 64
	MaxReleasesPerZone = 8
	MaxReleases        = MaxZones * MaxReleasesPerZone
	MaxDocumentPaths   = 64
	MaxDocumentBytes   = 64 << 10
)

type Binding struct {
	ID       string `json:"id"`
	Host     string `json:"host"`
	BasePath string `json:"basePath"`
}

// TopologyZone is native input to the opaque topology digest. Tenant is never
// copied into a ReleaseSpec or an ingress manifest.
type TopologyZone struct {
	Binding Binding `json:"binding"`
	Tenant  string  `json:"tenant"`
}

type ReleaseSpec struct {
	Version            int      `json:"version"`
	Zone               Binding  `json:"zone"`
	ReleaseID          string   `json:"releaseID"`
	RuntimeID          string   `json:"runtimeID"`
	PublicOrigin       string   `json:"publicOrigin"`
	AssetPrefix        string   `json:"assetPrefix"`
	AssetsSHA256       string   `json:"assetsSHA256"`
	TopologySHA256     string   `json:"topologySHA256"`
	DocumentPaths      []string `json:"documentPaths,omitempty"`
	NavigationProtocol int      `json:"navigationProtocol"`
	ActionProtocol     string   `json:"actionProtocol"`
}

type ReleaseManifest struct {
	Release      ReleaseSpec `json:"release"`
	BinarySHA256 string      `json:"binarySHA256"`
}

type IngressRelease struct {
	Artifact ReleaseManifest `json:"artifact"`
	Origin   string          `json:"origin"`
}

type IngressZone struct {
	Binding       Binding          `json:"binding"`
	ActiveRelease string           `json:"activeRelease"`
	Releases      []IngressRelease `json:"releases"`
}

// IngressManifest is a complete immutable routing snapshot. TopologySHA256 is
// an operator-supplied opaque anchor: public bindings cannot recompute a digest
// that also includes private native tenant metadata.
type IngressManifest struct {
	Version        int           `json:"version"`
	TopologySHA256 string        `json:"topologySHA256"`
	Zones          []IngressZone `json:"zones"`
}

func (v Binding) Clone() Binding {
	return Binding{ID: strings.Clone(v.ID), Host: strings.Clone(v.Host), BasePath: strings.Clone(v.BasePath)}
}

func (v TopologyZone) Clone() TopologyZone {
	return TopologyZone{Binding: v.Binding.Clone(), Tenant: strings.Clone(v.Tenant)}
}

func (v ReleaseSpec) Clone() ReleaseSpec {
	v.Zone = v.Zone.Clone()
	v.ReleaseID, v.RuntimeID = strings.Clone(v.ReleaseID), strings.Clone(v.RuntimeID)
	v.PublicOrigin, v.AssetPrefix = strings.Clone(v.PublicOrigin), strings.Clone(v.AssetPrefix)
	v.AssetsSHA256, v.TopologySHA256 = strings.Clone(v.AssetsSHA256), strings.Clone(v.TopologySHA256)
	v.ActionProtocol = strings.Clone(v.ActionProtocol)
	if v.DocumentPaths != nil {
		paths := make([]string, len(v.DocumentPaths))
		for i, path := range v.DocumentPaths {
			paths[i] = strings.Clone(path)
		}
		v.DocumentPaths = paths
	}
	return v
}

func (v ReleaseManifest) Clone() ReleaseManifest {
	return ReleaseManifest{Release: v.Release.Clone(), BinarySHA256: strings.Clone(v.BinarySHA256)}
}

func (v IngressRelease) Clone() IngressRelease {
	return IngressRelease{Artifact: v.Artifact.Clone(), Origin: strings.Clone(v.Origin)}
}

func (v IngressZone) Clone() IngressZone {
	v.Binding = v.Binding.Clone()
	v.ActiveRelease = strings.Clone(v.ActiveRelease)
	if v.Releases != nil {
		releases := make([]IngressRelease, len(v.Releases))
		for i, release := range v.Releases {
			releases[i] = release.Clone()
		}
		v.Releases = releases
	}
	return v
}

func (v IngressManifest) Clone() IngressManifest {
	v.TopologySHA256 = strings.Clone(v.TopologySHA256)
	if v.Zones != nil {
		zones := make([]IngressZone, len(v.Zones))
		for i, zone := range v.Zones {
			zones[i] = zone.Clone()
		}
		v.Zones = zones
	}
	return v
}
