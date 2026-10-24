package shared

import (
	"slices"
	"strings"
)

// Retention boundaries own the complete backing allocation of each string.
// A short request/CMS substring can otherwise pin an unaccounted large buffer.
func ownedID(id ID) ID {
	return ID{strings.Clone(id.Namespace), strings.Clone(id.Schema), strings.Clone(id.Key)}
}
func ownedPolicy(p Policy) Policy {
	p.Tags = slices.Clone(p.Tags)
	p.Paths = slices.Clone(p.Paths)
	for i, label := range p.Tags {
		p.Tags[i] = strings.Clone(label)
	}
	for i, label := range p.Paths {
		p.Paths[i] = strings.Clone(label)
	}
	return p
}
