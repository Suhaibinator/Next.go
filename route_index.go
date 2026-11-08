package nextgo

import "github.com/Suhaibinator/Next.go/internal/routing"

func (a *App) initRouteIndex() error {
	patterns := make([]string, len(a.routes))
	for i, route := range a.routes {
		patterns[i] = route.Pattern
	}
	index, err := routing.NewIndex(patterns)
	if err != nil {
		return err
	}
	a.routeIndex = index
	return nil
}
