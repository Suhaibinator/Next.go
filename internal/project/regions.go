package project

import "fmt"

// Request-time region registries initially belong to ordinary pages only. Run
// this after parallel URL synthesis so an ordinary page cannot accidentally
// acquire an unsupported parallel/default composition during slot attachment.
func validateRegionRoutes(p *Project) error {
	for _, route := range projectRoutes(p.Routes) {
		if route.Regions && (route.API || route.Default || route.Missing || len(route.Slots) > 0 || route.Primary != nil || len(route.Primaries) > 0) {
			return fmt.Errorf("regions.go requires an ordinary page without parallel/default/interception composition: %s", route.Import)
		}
	}
	return nil
}
