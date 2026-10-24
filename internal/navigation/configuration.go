package navigation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

const ConfigurationHeader = "X-Nextgo-Configuration"
const StaleHeader = "X-Nextgo-Stale-Millis"
const MaxStaleMillis = 24 * 60 * 60 * 1000

// ConfigurationID identifies the public ownership rules used by a document and
// navigation responses. Runtime environment changes need not change a bundle's
// build ID, so paths, endpoint selection and layout retention are versioned too.
// This is a compatibility check, not a signature or authorization mechanism.
func ConfigurationID(build string, deployment paths.Config, manifest Manifest, preserveLayouts, viewTransitions bool) string {
	encoded, _ := json.Marshal(struct {
		Version         int
		Build           string
		Paths           paths.Config
		Routes          Manifest
		PreserveLayouts bool
		ViewTransitions bool
	}{ProtocolVersion, build, deployment, manifest, preserveLayouts, viewTransitions})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func ValidConfigurationID(id string) bool {
	if len(id) != sha256.Size*2 {
		return false
	}
	for _, value := range id {
		if !(value >= '0' && value <= '9' || value >= 'a' && value <= 'f') {
			return false
		}
	}
	return true
}
