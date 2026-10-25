package zonecontract

import (
	"fmt"
	"net/http"
	"strings"
)

const (
	Protocol        = "1"
	HeaderProtocol  = "X-Nextgo-Zone-Protocol"
	HeaderZoneID    = "X-Nextgo-Zone-ID"
	HeaderReleaseID = "X-Nextgo-Zone-Release"
	HeaderTopology  = "X-Nextgo-Zone-Topology"
)

// Expectation is the private ingress-to-endpoint compatibility identity. It
// never authenticates an ingress, caller, application user or tenant member.
type Expectation struct {
	ZoneID, ReleaseID, TopologySHA256 string
}

func (value ReleaseSpec) Expectation() Expectation {
	return Expectation{ZoneID: strings.Clone(value.Zone.ID), ReleaseID: strings.Clone(value.ReleaseID), TopologySHA256: strings.Clone(value.TopologySHA256)}
}

func ValidateExpectation(value Expectation) error {
	if !validIdentifier(value.ZoneID, 64) || !validIdentifier(value.ReleaseID, 64) || !validHex(value.TopologySHA256, 64) {
		return fmt.Errorf("zone contract: invalid private release expectation")
	}
	return nil
}

func expectationFields(value Expectation) map[string]string {
	return map[string]string{HeaderProtocol: Protocol, HeaderZoneID: value.ZoneID, HeaderReleaseID: value.ReleaseID, HeaderTopology: value.TopologySHA256}
}

// CheckExpectation requires exactly one matching value per private header,
// including manually constructed case aliases that Header.Get would overlook.
// It applies equally to request headers and pre-body response identity headers;
// trailers cannot establish an identity after a response has committed.
func CheckExpectation(header http.Header, expected Expectation) error {
	if err := ValidateExpectation(expected); err != nil {
		return err
	}
	fields := expectationFields(expected)
	for name := range header {
		if !reservedHeader(name) {
			continue
		}
		known := false
		for field := range fields {
			known = known || strings.EqualFold(name, field)
		}
		if !known {
			return fmt.Errorf("zone contract: unknown or deferred private identity header")
		}
	}
	for field, want := range fields {
		var values []string
		names := 0
		for name, actual := range header {
			if strings.EqualFold(name, field) {
				names++
				values = append(values, actual...)
			}
		}
		if names != 1 || len(values) != 1 || values[0] != want {
			return fmt.Errorf("zone contract: missing, duplicate or mismatched private identity")
		}
	}
	return nil
}

func reservedHeader(name string) bool {
	name = strings.ToLower(name)
	name = strings.TrimPrefix(name, strings.ToLower(http.TrailerPrefix))
	return strings.HasPrefix(name, "x-nextgo-zone-")
}

// StripExpectation also removes unknown private fields and trailer names, so
// untrusted aliases or future private extensions cannot cross the app boundary.
func StripExpectation(header http.Header) {
	for name := range header {
		if reservedHeader(name) {
			delete(header, name)
			continue
		}
		if !strings.EqualFold(name, "Trailer") {
			continue
		}
		var kept []string
		for _, line := range header[name] {
			for _, field := range strings.Split(line, ",") {
				field = strings.TrimSpace(field)
				if field != "" && !reservedHeader(field) {
					kept = append(kept, field)
				}
			}
		}
		if len(kept) == 0 {
			delete(header, name)
		} else {
			header[name] = []string{strings.Join(kept, ", ")}
		}
	}
}

// SetExpectation replaces every private case alias with exact singleton fields.
// Callers use a validated expectation from a normalized ReleaseSpec.
func SetExpectation(header http.Header, expected Expectation) {
	StripExpectation(header)
	if header == nil {
		return
	}
	for name, value := range expectationFields(expected) {
		header.Set(name, value)
	}
}
