package nextgo

import (
	"encoding/json"
	"net/http"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

// RouteRetention explicitly permits keeping this response's UI in the current
// browser document. Partition must be a bounded opaque PUBLIC identity boundary
// supplied by the application; it is neither a session token nor an access grant.
// The browser still obtains a fresh server response before restoring hidden UI.
// Changed server HTML presently cold-renders; state-preserving reconciliation is
// a later capability. Unsupported compositions keep ordinary fresh rendering.
type RouteRetention struct {
	Partition string
}

func (a *App) retentionDescriptor(p Page, status int, body string) (*navigation.Retention, error) {
	if !a.config.RetainRoutes || p.Retention == nil || status != http.StatusOK ||
		a.config.Dev || a.config.Phase != PhaseProduction || a.config.Draft != nil || a.config.Offline != nil ||
		a.config.StaticShell != nil || a.config.PreserveLayouts || a.config.Locales != nil || a.config.localeScope != nil ||
		p.Stream || p.DeferredMetadata != nil || p.deferredPlan != nil || p.partial != nil {
		return nil, nil
	}
	return navigation.NewRetention(p.Retention.Partition, body)
}

func retentionJSON(value *navigation.Retention) string {
	if value == nil {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}
