//go:build !js

package photos

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

type Photo struct{ ID, Title, Description string }

func Load(c *nextgo.Context) (Photo, error) {
	if err := c.Context.Err(); err != nil {
		return Photo{}, err
	}
	// Demonstration probes, not a substitute for an application's identity policy.
	if c.Request.Header.Get("X-Demo-Photo-Deny") == "yes" {
		return Photo{}, nextgo.ErrForbidden
	}
	for _, p := range []Photo{{"one", "Coastal light", "A quiet coastline at the end of the day."}, {"two", "Mountain air", "A clear morning above the valley."}} {
		if p.ID == c.Params["id"] {
			if c.Request.Header.Get("X-Demo-Photo-Version") == "updated" {
				p.Description = "Updated on the server with the current request."
			}
			return p, nil
		}
	}
	return Photo{}, nextgo.ErrNotFound
}
func View(p Photo) ui.Node {
	return ui.Fragment(ui.El("div", ui.Attrs{"class": "photo-art photo-" + p.ID, "role": "img", "aria-label": p.Description}, ui.Text(p.Title)), ui.El("p", nil, ui.Text(p.Description)))
}
