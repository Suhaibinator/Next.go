//go:build !js

package status

import (
	"github.com/Suhaibinator/Next.go/examples/zones/site"
	"net/http"
)

func Handle(w http.ResponseWriter, r *http.Request) { site.Status(w, r, "blog") }
