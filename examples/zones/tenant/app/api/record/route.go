//go:build !js

package record

import (
	"github.com/Suhaibinator/Next.go/examples/zones/tenantdata"
	"net/http"
)

func Handle(w http.ResponseWriter, r *http.Request) { tenantdata.API(w, r) }
