package shared

import (
	"crypto/sha256"
	"encoding/hex"
)

// NamespaceIdentity identifies the namespace-wide invalidation domain as an
// opaque digest. Unlike ScopeID, it deliberately excludes the serialization
// schema: Expire/MarkStale operate across every schema in a namespace. It also
// excludes backend endpoints and credentials, so equal identities can refer to
// physically separate services; consumers may conservatively reject that reuse.
// This is comparison metadata, not authorization or proof of backend isolation.
// Nil/unconstructed stores have no identity.
func (s *Store) NamespaceIdentity() string {
	if s == nil || s.backend == nil || !validName(s.namespace) || !validName(s.schema) {
		return ""
	}
	sum := sha256.Sum256([]byte("nextgo.shared.namespace.v1\x00" + s.namespace))
	return hex.EncodeToString(sum[:])
}
