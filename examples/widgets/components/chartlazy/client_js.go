//go:build js && wasm

package chartlazy

import (
	"encoding/json"

	"github.com/Suhaibinator/Next.go/client"
	"github.com/Suhaibinator/Next.go/examples/widgets/components/chart"
)

func Mount(scope *client.Scope, raw json.RawMessage) error { return chart.Mount(scope, raw) }
