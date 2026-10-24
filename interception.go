package nextgo

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/routing"
)

type interceptionOwner struct {
	route, slot int
	primary     PrimarySlot
}
type interceptionView struct {
	url   *url.URL
	route Route
	slot  string
}

// InterceptedFrom returns the selected background's logical URL, including its
// query, inside an interception page or its boundaries. It is public view data,
// not an authorization result. Use URL/MustURL before passing it to a browser.
func (c *Context) InterceptedFrom() (string, bool) { return c.interceptFrom, c.interceptFrom != "" }

func (a *App) initInterceptions() error {
	for i, r := range a.routes {
		claims := map[string][]string{}
		for j, slot := range r.Slots {
			if len(slot.Intercepts) == 0 {
				continue
			}
			var anchor *PrimarySlot
			for _, p := range primaryChain(r) {
				if p.Layout == slot.Layout && p.Base == slot.Base {
					copy := p
					anchor = &copy
					break
				}
			}
			if anchor == nil {
				return fmt.Errorf("slot %s: interception requires its owner's primary declaration", slot.Name)
			}
			for _, branch := range slot.Intercepts {
				registered := false
				for _, canonical := range a.routes {
					if canonical.API == nil {
						if routing.Covers(canonical.Pattern, branch.Pattern) {
							registered = true
							break
						}
					}
				}
				if !registered {
					return fmt.Errorf("intercepted target %s requires a normal page route", branch.Pattern)
				}
				claims[anchor.ID] = append(claims[anchor.ID], branch.Pattern)
			}
			a.intercepts = append(a.intercepts, interceptionOwner{i, j, *anchor})
		}
		for _, patterns := range claims {
			if err := routing.ValidatePatterns(patterns); err != nil {
				return fmt.Errorf("interception ambiguity: %w", err)
			}
		}
	}
	return nil
}

// Shared parameter names in an owner base bind the destination to that same
// concrete scope. A carrier cannot select another tenant/account's background.
func interceptionParams(slot Slot, background *url.URL, pattern string, target *url.URL) (map[string]string, bool) {
	params, ok := routing.Match(pattern, target.EscapedPath())
	if !ok {
		return nil, false
	}
	prefix, ok := slotOwnerPrefix(slot.Base, background.EscapedPath())
	if !ok {
		return nil, false
	}
	owner, _ := routing.Match(slot.Base, prefix)
	for name, value := range owner {
		if selected, exists := params[name]; exists && selected != value {
			return nil, false
		}
	}
	return params, true
}
func (a *App) interceptionTarget(target *url.URL) bool {
	for _, owner := range a.intercepts {
		for _, branch := range a.routes[owner.route].Slots[owner.slot].Intercepts {
			if _, ok := routing.Match(branch.Pattern, target.EscapedPath()); ok {
				return true
			}
		}
	}
	return false
}

// selectInterception accepts only registered primary selections. Public state
// chooses a view; rendering re-runs every loader using the current request.
func (a *App) selectInterception(request *http.Request) (*interceptionView, error) {
	if len(a.intercepts) == 0 || request.Header.Get("X-Nextgo-Navigate") != "1" || !a.interceptionTarget(request.URL) {
		return nil, nil
	}
	values := request.Header.Values(navigation.SlotsHeader)
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) != 1 {
		return nil, fmt.Errorf("multiple interception carriers")
	}
	state, err := navigation.ParseSlotState(values[0])
	if err != nil {
		return nil, err
	}
	if state.Configuration != a.configurationID {
		return nil, fmt.Errorf("incompatible interception carrier")
	}
	var selected *interceptionView
	type origin struct {
		key string
		url *url.URL
	}
	origins := map[int][]origin{}
	for key, raw := range state.Selections {
		candidate, _ := url.Parse(raw)
		for i, r := range a.routes {
			if _, ok := routing.Match(r.Pattern, candidate.EscapedPath()); ok {
				origins[i] = append(origins[i], origin{key, candidate})
				break
			}
		}
	}
	depth := -1
	selectedPattern, selectedShape := "", ""
	ambiguous := false
	for _, owner := range a.intercepts {
		candidateRoute := a.routes[owner.route]
		slot := candidateRoute.Slots[owner.slot]
		for _, origin := range origins[owner.route] {
			key, candidate := origin.key, origin.url
			parent := ""
			anchor := ""
			for _, p := range primaryChain(candidateRoute) {
				if p.Layout > owner.primary.Layout {
					break
				}
				scope, ok := scopedSlot(Slot{ID: p.ID, Base: p.Base}, candidate.EscapedPath(), parent)
				if !ok {
					break
				}
				parent = scope
				if p.ID == owner.primary.ID {
					anchor = scope
				}
			}
			if anchor == "" || anchor != key {
				continue
			}
			for _, branch := range slot.Intercepts {
				if _, ok := interceptionParams(slot, candidate, branch.Pattern, request.URL); !ok {
					continue
				}
				shape, _ := routing.Shape(branch.Pattern)
				prefer := owner.primary.Layout > depth || owner.primary.Layout == depth && shape != selectedShape && routing.Less(branch.Pattern, selectedPattern)
				if prefer {
					selected = &interceptionView{candidate, candidateRoute, slot.ID}
					depth = owner.primary.Layout
					selectedPattern, selectedShape = branch.Pattern, shape
					ambiguous = false
				} else if owner.primary.Layout == depth && shape == selectedShape && selected != nil && (selected.slot != slot.ID || selected.url.RequestURI() != candidate.RequestURI()) {
					ambiguous = true
				}
			}
		}
	}
	if ambiguous {
		return nil, fmt.Errorf("ambiguous interception background")
	}
	return selected, nil
}
