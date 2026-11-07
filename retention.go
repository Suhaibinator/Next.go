package nextgo

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/renderowner"
	"github.com/Suhaibinator/Next.go/internal/routing"
	"github.com/Suhaibinator/Next.go/ui"
)

// RouteRetention explicitly permits keeping this response's UI in the current
// browser document. Partition must be a bounded opaque PUBLIC identity boundary
// supplied by the application; it is neither a session token nor an access grant.
// The browser still obtains a fresh server response before restoring hidden UI.
// Compatible opt-in client-view roots can retain stationary local UI while
// fresh server output/props commit. Ordinary changed-body islands and unsupported
// compositions use fresh rendering; general reconciliation remains future work.
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

// Each origin is captured before author callbacks; public Request/Params cannot
// retrospectively change an identity. It introduces no HTML wrapper.
type routeMergeOrigin struct {
	page    renderowner.Frame
	layouts []renderowner.Frame
}
type routeOwnerNode struct {
	frame renderowner.Frame
	child ui.Node
}

func (n routeOwnerNode) Render(w io.Writer) error {
	if n.child == nil {
		return nil
	}
	ctx := ui.RenderContext(w)
	if renderowner.RecorderFrom(ctx) == nil {
		return n.child.Render(w)
	}
	if n.frame.Kind == "" {
		renderowner.RecorderFrom(ctx).Disable()
		return n.child.Render(w)
	}
	return ui.RenderTo(renderowner.With(ctx, n.frame), w, n.child)
}
func mergeOriginFields(id, pattern string, params map[string]string) []string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fields := []string{strings.Clone(id), strings.Clone(pattern)}
	for _, key := range keys {
		fields = append(fields, strings.Clone(key), strings.Clone(params[key]))
	}
	return fields
}
func snapshotRouteMerge(c *Context, r Route) *routeMergeOrigin {
	result := &routeMergeOrigin{}
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return result
	}
	// Match a private original pathname, rather than accepting later public map
	// writes as route authority. The selected pattern already passed routing.
	path := strings.Clone(c.Request.URL.EscapedPath())
	params, ok := routing.Match(r.Pattern, path)
	if !ok {
		return result
	}
	result.page = renderowner.Semantic("page", mergeOriginFields(r.Pattern, r.Pattern, params)...)
	result.layouts = make([]renderowner.Frame, len(r.Layouts))
	seenIDs := map[string]bool{}
	previousPrefix := ""
	for i := range r.Layouts {
		if len(r.LayoutIDs) != len(r.Layouts) || len(r.LayoutBases) != len(r.Layouts) || r.LayoutIDs[i] == "" || seenIDs[r.LayoutIDs[i]] {
			return result
		}
		seenIDs[r.LayoutIDs[i]] = true
		prefix, ok := slotOwnerPrefix(r.LayoutBases[i], path)
		if !ok {
			continue
		}
		owned, ok := routing.Match(r.LayoutBases[i], prefix)
		if !ok {
			continue
		}
		if previousPrefix != "" && prefix != previousPrefix && !strings.HasPrefix(prefix, strings.TrimSuffix(previousPrefix, "/")+"/") {
			return result
		}
		previousPrefix = prefix
		result.layouts[i] = renderowner.Semantic("layout", mergeOriginFields(r.LayoutIDs[i], r.LayoutBases[i], owned)...)
	}
	return result
}
func completeMergeOrigin(value *routeMergeOrigin) bool {
	if value == nil || value.page.Kind != "page" || value.page.ID == "" {
		return false
	}
	for _, frame := range value.layouts {
		if frame.Kind != "layout" || frame.ID == "" {
			return false
		}
	}
	return true
}
func mergePagePartition(p Page) string {
	if p.mergePartition != "" {
		return p.mergePartition
	}
	if p.Retention != nil {
		return p.Retention.Partition
	}
	return ""
}
func (a *App) mergeResponseEligible(r *http.Request, p Page, status int) bool {
	if r == nil || !a.config.RetainRoutes || mergePagePartition(p) == "" || status != http.StatusOK || a.config.Dev || a.config.Phase != PhaseProduction || a.config.PrerenderBuildMode || a.config.Draft != nil || a.config.Offline != nil || a.config.StaticShell != nil || a.config.PreserveLayouts || a.config.Locales != nil || a.config.localeScope != nil || p.Stream || p.DeferredMetadata != nil || p.deferredPlan != nil || p.partial != nil || offlineCaptureFor(r) != nil {
		return false
	}
	if _, capture := r.Context().Value(prerenderCaptureKey{}).(*prerenderCapture); capture {
		return false
	}
	return true
}
func mergeJSON(value *navigation.MergeDescriptor) string {
	if value == nil {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}
