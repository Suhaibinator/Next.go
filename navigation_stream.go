package nextgo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func (a *App) respondNavigationStream(w http.ResponseWriter, r *http.Request, page Page, head []seo.Element, document Document) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	w.Header().Set("Content-Type", navigation.StreamMediaType+"; charset=utf-8")
	w.Header().Set(navigation.StreamHeader, fmt.Sprint(navigation.StreamVersion))
	w.Header().Set(navigation.ProtocolHeader, fmt.Sprint(navigation.ProtocolVersion))
	w.Header().Set(navigation.ConfigurationHeader, a.configurationID)
	w.Header().Set("Cache-Control", "private, no-store")
	nonceResponse(w, r)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Add("Vary", "X-Nextgo-Navigate, X-Nextgo-Stream, X-Nextgo-Purpose")
	clearNavigationReuse(w.Header())
	a.draftResponse(w, r)
	committed, used := false, 0
	write := func(frame navigation.StreamFrame) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		frame.Version = navigation.StreamVersion
		encoded, err := json.Marshal(frame)
		if err != nil {
			return err
		}
		if len(encoded) > navigation.MaxStreamFrame || len(encoded)+1 > navigation.MaxStreamBytes-used {
			return fmt.Errorf("navigation stream exceeds byte limits")
		}
		encoded = append(encoded, '\n')
		used += len(encoded)
		committed = true
		n, err := w.Write(encoded)
		if err == nil && n != len(encoded) {
			err = io.ErrShortWrite
		}
		if err != nil {
			return err
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return nil
	}
	err := ui.RenderEvents(ctx, page.Body, func(event ui.StreamEvent) error {
		if event.ID == "" {
			payload, err := json.Marshal(Envelope{Version: NavigationProtocolVersion, Configuration: a.configurationID, Document: document, Build: a.config.BuildID, URL: requestURI(r), HTML: string(event.HTML), Title: page.Title, Description: page.Description, Meta: page.Meta, Status: 200, Head: head})
			if err != nil {
				return err
			}
			return write(navigation.StreamFrame{Type: "shell", Page: payload})
		}
		if event.Err != nil {
			a.config.Logger.Printf("deferred boundary %s: %v", event.ID, event.Err)
		}
		return write(navigation.StreamFrame{Type: "chunk", ID: event.ID, HTML: string(event.HTML), Failed: event.Err != nil})
	})
	if err == nil {
		err = write(navigation.StreamFrame{Type: "done"})
	}
	if err != nil {
		a.config.Logger.Printf("navigation stream: %v", err)
		if !committed {
			w.Header().Del(navigation.StreamHeader)
			a.respond(w, r, errorPage(), 500)
		}
	}
}
