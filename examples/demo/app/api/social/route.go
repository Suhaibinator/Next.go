//go:build !js

package social

import (
	"context"
	"net/http"
	"net/url"

	"github.com/Suhaibinator/Next.go/examples/demo/socialcard"
	"github.com/Suhaibinator/Next.go/socialimage"
)

var cards = func() *socialimage.Handler {
	h, err := socialimage.NewHandler(socialimage.Config{Public: true}, func(ctx context.Context, r *http.Request) (socialimage.Document, error) {
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return socialimage.Document{}, socialimage.ErrBadRequest
		}
		title, description, err := socialcard.Query(q)
		if err != nil {
			return socialimage.Document{}, err
		}
		return socialcard.Document(title, description), ctx.Err()
	})
	if err != nil {
		panic(err)
	} // Fixed demonstration configuration.
	return h
}()

func Handle(w http.ResponseWriter, r *http.Request) { cards.ServeHTTP(w, r) }
