package project

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/Suhaibinator/Next.go/internal/actionwire"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/zonecontract"
)

// ZoneReleaseBuild contains one independently compiled application and its
// public release contract. No peer application's Go package is imported.
type ZoneReleaseBuild struct {
	*Build
	Manifest zonecontract.ReleaseManifest
}

type zoneReleasePlan struct {
	topologyPath   string
	selected       ZoneManifest
	spec           zonecontract.ReleaseSpec
	peerRoots      []string
	inputs         *zoneReleaseInputs
	inputTemplates map[string]string
}

func prepareZoneRelease(manifestPath, zoneID, publicOrigin, releaseID string) (*zoneReleasePlan, error) {
	topology, err := DiscoverZones(manifestPath)
	if err != nil {
		return nil, err
	}
	var selected *ZoneManifest
	inventory := make([]zonecontract.TopologyZone, 0, len(topology.Zones))
	bindings := make([]zonecontract.Binding, 0, len(topology.Zones))
	for i := range topology.Zones {
		zone := &topology.Zones[i]
		binding := zonecontract.Binding{ID: zone.ID, Host: zone.Host, BasePath: zone.BasePath}
		inventory = append(inventory, zonecontract.TopologyZone{Binding: binding, Tenant: zone.Tenant})
		bindings = append(bindings, binding)
		if zone.ID == zoneID {
			selected = zone
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("zone release requires one exact declared zone ID")
	}
	if releaseID == "" {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, fmt.Errorf("allocate zone release identity: %w", err)
		}
		releaseID = hex.EncodeToString(nonce[:])
	}
	prefix, err := zonecontract.ReleasePrefix(selected.BasePath, releaseID)
	if err != nil {
		return nil, err
	}
	digest, err := zonecontract.TopologyDigest(inventory)
	if err != nil {
		return nil, err
	}
	binding := zonecontract.Binding{ID: selected.ID, Host: selected.Host, BasePath: selected.BasePath}
	documents, err := zonecontract.NestedDocumentPaths(binding, bindings)
	if err != nil {
		return nil, err
	}
	// Runtime/asset identities are assigned only after the ordinary selected-app
	// browser build. Placeholders allow validating every operator input first.
	spec, err := zonecontract.NormalizeReleaseSpec(zonecontract.ReleaseSpec{
		Version: zonecontract.Version, Zone: binding, ReleaseID: releaseID,
		RuntimeID: "00000000000000000000", PublicOrigin: publicOrigin,
		AssetPrefix: prefix, TopologySHA256: digest,
		AssetsSHA256:  "0000000000000000000000000000000000000000000000000000000000000000",
		DocumentPaths: documents, NavigationProtocol: navigation.ProtocolVersion, ActionProtocol: actionwire.Protocol,
	})
	if err != nil {
		return nil, err
	}
	plan := &zoneReleasePlan{topologyPath: topology.Path, selected: *selected, spec: spec}
	for _, zone := range topology.Zones {
		if zone.Root != selected.Root {
			addUnique(&plan.peerRoots, zone.Root)
		}
	}
	return plan, nil
}

func (plan *zoneReleasePlan) validateProject(p *Project) error {
	if len(p.MetadataFiles) != 0 {
		return fmt.Errorf("independent zone releases do not yet support managed metadata files")
	}
	if hasPrerenderFiles(p) {
		return fmt.Errorf("independent zone releases do not yet support prerender declarations; no factory or capture was executed")
	}
	return nil
}

func (plan *zoneReleasePlan) validateTopology() error {
	current, err := prepareZoneRelease(plan.topologyPath, plan.selected.ID, plan.spec.PublicOrigin, plan.spec.ReleaseID)
	if err != nil {
		return err
	}
	if current.spec.TopologySHA256 != plan.spec.TopologySHA256 || current.selected.Root != plan.selected.Root {
		return fmt.Errorf("zone topology or selected application changed during release compilation")
	}
	return nil
}

// CompileZoneRelease compiles only the selected application's ordinary native
// and browser graphs. Unused workspace and replacement source/asset trees do
// not enter its fingerprint. An explicitly selected active Go workspace remains
// compiler policy, including Go's SDK-selection and module-version requirements.
func CompileZoneRelease(ctx context.Context, manifestPath, zoneID, publicOrigin, releaseID string, output io.Writer) (*ZoneReleaseBuild, error) {
	plan, err := prepareZoneRelease(manifestPath, zoneID, publicOrigin, releaseID)
	if err != nil {
		return nil, err
	}
	build, err := compileApplication(ctx, plan.selected.Root, false, output, false, false, plan)
	if err != nil {
		return nil, err
	}
	return finishZoneRelease(ctx, build, plan)
}
