package scripts

import (
	"fmt"

	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

// Module selects a trusted JavaScript module for native browser import().
// Unlike Script, it has no integrity/credential/referrer/timing attributes:
// native dynamic import does not expose those controls. Use versioned URLs
// and an appropriate document CSP.
type Module struct {
	Asset bool   `json:"asset,omitempty"`
	ID    string `json:"id"`
	Src   string `json:"src"`
}

func (m Module) Check() (Module, error) {
	_, err := (Script{Asset: m.Asset, ID: m.ID, Src: m.Src, Type: "module"}).Check()
	return m, err
}

// ParseModule rejects unknown fields, including unsupported script security
// attributes, instead of suggesting that native import honors them.
func ParseModule(raw string) (Module, error) {
	var module Module
	if len(raw) > 8192 {
		return module, fmt.Errorf("modules: declaration exceeds 8 KiB")
	}
	if err := wirejson.Unique([]byte(raw)); err != nil {
		return module, fmt.Errorf("modules: invalid declaration: %w", err)
	}
	if err := wirejson.Strict([]byte(raw), &module); err != nil {
		return module, fmt.Errorf("modules: invalid declaration: %w", err)
	}
	return module.Check()
}
