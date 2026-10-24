// Package prerenderid binds trusted build snapshots to their executable.
package prerenderid

import (
	"crypto/sha256"
	"encoding/hex"
)

func Build(code string, data []byte) string {
	sum := sha256.Sum256(append([]byte("nextgo-prerender-v1\x00"+code+"\x00"), data...))
	return hex.EncodeToString(sum[:])[:20]
}
