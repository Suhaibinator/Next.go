package project

import (
	"fmt"
	"github.com/Suhaibinator/Next.go/internal/routing"
)

func validateFileInterceptions(p *Project) error {
	for _, source := range p.Routes {
		claims := map[int][]string{}
		for _, slot := range source.Slots {
			if len(slot.Intercepts) > 0 && slot.Default == "" && slot.DefaultRoute == nil {
				return fmt.Errorf("interception slot %s requires default.go", slot.Name)
			}
			for _, branch := range slot.Intercepts {
				registered := false
				for _, canonical := range p.Routes {
					if !canonical.API {
						if routing.Covers(canonical.Pattern, branch.Pattern) {
							registered = true
							break
						}
					}
				}
				if !registered {
					return fmt.Errorf("intercepted target %s requires a normal page.go", branch.Pattern)
				}
				claims[slot.Layout] = append(claims[slot.Layout], branch.Pattern)
			}
		}
		for _, patterns := range claims {
			if err := routing.ValidatePatterns(patterns); err != nil {
				return fmt.Errorf("ambiguous interception: %w", err)
			}
		}
	}
	return nil
}
