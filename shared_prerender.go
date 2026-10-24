package nextgo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/Suhaibinator/Next.go/cache/shared"
)

func (a *App) sharedPrerenderBinding(route Route, logical string) string {
	data, _ := json.Marshal([]string{"nextgo.prerender.v1", a.config.BuildID, a.configurationID, route.Pattern, logical})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func (a *App) sharedPrerenderStores(scopes []prerenderScope) map[string]*shared.Store {
	stores := map[string]*shared.Store{}
	for _, scope := range scopes {
		for _, r := range scope.app.routes {
			if r.Prerender != nil && r.Prerender.Shared {
				store := scope.app.config.SharedCache
				stores[store.ScopeID()] = store
			}
		}
	}
	return stores
}
func (a *App) reservePrerenderSeeds(ctx context.Context) ([]prerenderSeedReservation, error) {
	stores := a.sharedPrerenderStores(a.prerenderScopes())
	if len(stores) > shared.MaxSeedTickets {
		return nil, fmt.Errorf("nextgo: too many shared prerender scopes")
	}
	keys := slices.Sorted(maps.Keys(stores))
	result := make([]prerenderSeedReservation, 0, len(keys))
	for _, scope := range keys {
		ticket, err := stores[scope].ReserveSeeds(ctx)
		if err != nil {
			return nil, fmt.Errorf("nextgo: reserve shared prerender build: %w", err)
		}
		if shared.ValidateSeedToken(ticket.Token) != nil {
			return nil, fmt.Errorf("nextgo: invalid shared prerender reservation")
		}
		result = append(result, prerenderSeedReservation{Scope: scope, Ticket: ticket.Token})
	}
	return result, nil
}
func (a *App) initSharedPrerenderSeeds(manifest prerenderManifest, scopes []prerenderScope) error {
	if len(manifest.SharedSeeds) > shared.MaxSeedTickets {
		return fmt.Errorf("nextgo: too many shared prerender reservations")
	}
	tickets := map[string]string{}
	for _, r := range manifest.SharedSeeds {
		raw, err := hex.DecodeString(r.Scope)
		if err != nil || len(raw) != sha256.Size || tickets[r.Scope] != "" || shared.ValidateSeedToken(r.Ticket) != nil {
			return fmt.Errorf("nextgo: invalid shared prerender reservation")
		}
		tickets[r.Scope] = r.Ticket
	}
	stores := a.sharedPrerenderStores(scopes)
	groups := map[string][]shared.Seed[sharedPageSnapshot]{}
	seen := map[string]map[string]bool{}
	for _, scope := range scopes {
		app := scope.app
		store := app.config.SharedCache
		identity := store.ScopeID()
		if tickets[identity] == "" {
			continue
		}
		if seen[identity] == nil {
			seen[identity] = map[string]bool{}
		}
		for _, p := range manifest.Pages {
			if p.Shape != app.prerenderShape {
				continue
			}
			var route *Route
			for i := range app.routes {
				if app.routes[i].Pattern == p.Pattern {
					route = &app.routes[i]
					break
				}
			}
			if route == nil || route.Prerender == nil || !route.Prerender.Shared {
				continue
			}
			if len(prerenderDecodedPath(p.Path)) > 512 {
				continue
			}
			binding := app.sharedPrerenderBinding(*route, p.Path)
			if seen[identity][binding] {
				continue
			}
			seen[identity][binding] = true
			value := cachedPage{Page: Page{Title: p.Title, Description: p.Description, Meta: maps.Clone(p.Meta), document: &p.Document, preparedHead: &renderedHead{elements: p.Head, markup: p.HeadHTML}}, HTML: p.HTML, GeneratedAt: p.GeneratedAt, Prerendered: true}
			dto, err := encodeSharedPage(value, binding)
			if err != nil {
				return fmt.Errorf("nextgo: encode shared prerender seed: %w", err)
			}
			groups[identity] = append(groups[identity], shared.Seed[sharedPageSnapshot]{Key: binding, Value: dto, Policy: sharedPagePolicy(route.Cache, route.Cache.Lifetime, prerenderDecodedPath(p.Path))})
		}
	}
	// Consume even empty/admission-skipped batches. A used build artifact must
	// never become a future opportunity to refill an invalidated/evicted hole.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, scope := range slices.Sorted(maps.Keys(stores)) {
		if tickets[scope] == "" {
			continue
		}
		if _, err := shared.Bootstrap(ctx, stores[scope], tickets[scope], groups[scope]); err != nil {
			return fmt.Errorf("nextgo: initialize shared prerender seeds: %w", err)
		}
	}
	return nil
}
