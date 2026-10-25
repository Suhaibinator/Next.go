package nextgo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Phase identifies the native startup purpose. Generated entrypoints set it
// before calling Configure. It is not inferred from request or public data.
type Phase string

const (
	PhaseDevelopment  Phase = "development"
	PhaseProduction   Phase = "production"
	PhasePrerender    Phase = "prerender"
	PhaseStaticExport Phase = "static-export"
)

func normalizeConfigPhase(c *Config) error {
	if c.Dev && c.PrerenderBuildMode {
		return fmt.Errorf("nextgo: development and prerender build modes conflict")
	}
	if c.Phase == "" {
		switch {
		case c.PrerenderBuildMode:
			c.Phase = PhasePrerender
		case c.Dev:
			c.Phase = PhaseDevelopment
		default:
			c.Phase = PhaseProduction
		}
	}
	switch c.Phase {
	case PhaseDevelopment:
		if !c.Dev || c.PrerenderBuildMode {
			return fmt.Errorf("nextgo: development phase requires Dev and forbids prerender build mode")
		}
	case PhaseProduction, PhaseStaticExport:
		if c.Dev || c.PrerenderBuildMode {
			return fmt.Errorf("nextgo: %s phase requires development and prerender build modes to be disabled", c.Phase)
		}
	case PhasePrerender:
		if !c.PrerenderBuildMode || c.Dev {
			return fmt.Errorf("nextgo: prerender phase requires PrerenderBuildMode and forbids Dev")
		}
	default:
		return fmt.Errorf("nextgo: unknown configuration phase %q", c.Phase)
	}
	return nil
}

// Production and its build probe deliberately retain the same snapshot shape.
// Export namespaces the existing opaque cache-policy identity. Keeping that
// value in the manifest lets the browser validate the normal configuration ID,
// without publishing a phase field or consuming production seeds.
func configurationPhasePolicy(phase Phase, id string) string {
	if phase != PhaseStaticExport {
		return id
	}
	sum := sha256.Sum256([]byte("nextgo.cache.phase.v1\x00static-export\x00" + id))
	return hex.EncodeToString(sum[:])
}
