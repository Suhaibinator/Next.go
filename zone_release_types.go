package nextgo

import "github.com/Suhaibinator/Next.go/internal/zonecontract"

// These aliases share a single versioned wire contract with the compiler and
// ingress. They describe trusted deployment policy, not authentication claims.
type ZoneBinding = zonecontract.Binding
type ZoneReleaseSpec = zonecontract.ReleaseSpec
type ZoneReleaseManifest = zonecontract.ReleaseManifest
type ZoneIngressManifest = zonecontract.IngressManifest
type ZoneIngressRelease = zonecontract.IngressRelease
type ZoneIngressZone = zonecontract.IngressZone
