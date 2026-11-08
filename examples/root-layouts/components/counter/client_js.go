//go:build js && wasm

package counter

import (
	"encoding/json"
	"github.com/Suhaibinator/Next.go/client"
	original "github.com/Suhaibinator/Next.go/examples/metadata-site/components/counter"
)

func Mount(s *client.Scope, raw json.RawMessage) error { return original.Mount(s, raw) }
