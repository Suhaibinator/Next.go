package zonecontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

func NormalizeTopology(input []TopologyZone) ([]TopologyZone, error) {
	if len(input) == 0 || len(input) > MaxZones {
		return nil, fmt.Errorf("zone contract: require 1 to %d topology zones", MaxZones)
	}
	bindings := make([]Binding, len(input))
	for i, zone := range input {
		if zone.Tenant != "" && !validIdentifier(zone.Tenant, 128) {
			return nil, fmt.Errorf("zone contract: invalid bounded native tenant ID")
		}
		bindings[i] = zone.Binding
	}
	bindings, err := normalizeBindings(bindings)
	if err != nil {
		return nil, err
	}
	owned := make([]TopologyZone, len(input))
	for i, zone := range input {
		owned[i] = TopologyZone{Binding: bindings[i], Tenant: strings.Clone(zone.Tenant)}
	}
	slices.SortFunc(owned, func(a, b TopologyZone) int { return strings.Compare(a.Binding.ID, b.Binding.ID) })
	return owned, nil
}

// TopologyDigest hashes normalized native ownership, including tenant metadata.
// Source directories, application versions and SDKs deliberately do not enter
// this identity, so an unchanged topology permits independent app releases.
func TopologyDigest(input []TopologyZone) (string, error) {
	owned, err := NormalizeTopology(input)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(owned)
	if err != nil {
		return "", err
	}
	sum := sha256.New()
	_, _ = sum.Write([]byte("nextgo.zone.topology.v1\x00"))
	_, _ = sum.Write(data)
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// NestedDocumentPaths derives complete literal boundaries without inspecting,
// importing, or executing any peer application. The selected binding must be
// an exact member of the validated public inventory.
func NestedDocumentPaths(selected Binding, inventory []Binding) ([]string, error) {
	selected, err := NormalizeBinding(selected)
	if err != nil {
		return nil, err
	}
	bindings, err := normalizeBindings(inventory)
	if err != nil {
		return nil, err
	}
	found := false
	var documents []string
	for _, binding := range bindings {
		if binding == selected {
			found = true
			continue
		}
		if binding.Host == selected.Host {
			if logical, inside := paths.Strip(selected.BasePath, binding.BasePath); inside {
				documents = append(documents, logical)
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("zone contract: selected binding is absent from topology")
	}
	return normalizeDocumentPaths(documents)
}
