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
	shell, shellRef := partialEnvelopeFields(r, page, http.StatusOK, true)
	if shell != "" {
		w.Header().Set(navigation.ShellProtocolHeader, fmt.Sprint(navigation.ShellVersion))
	}
	a.draftResponse(w, r)
	committed, used := false, 0
	writeEncoded := func(encoded []byte, terminal bool) error {
		if err := ctx.Err(); err != nil && !terminal {
			return err
		}
		if terminal && page.deferredPlan != nil && page.deferredPlan.context.Err() != nil {
			return page.deferredPlan.context.Err()
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
	write := func(frame navigation.StreamFrame) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		frame.Version = navigation.StreamVersion
		encoded, err := json.Marshal(frame)
		if err != nil {
			return err
		}
		return writeEncoded(encoded, false)
	}
	emitBody := func(event ui.StreamEvent) error {
		if event.ID == "" {
			html := string(event.HTML)
			if shellRef {
				html = ""
			}
			payload, err := json.Marshal(Envelope{Shell: shell, ShellRef: shellRef, Version: NavigationProtocolVersion, Configuration: a.configurationID, Document: document, Build: a.config.BuildID, URL: requestURI(r), HTML: html, Title: page.Title, Description: page.Description, Meta: page.Meta, Status: 200, Head: head})
			if err != nil {
				return err
			}
			frame := navigation.StreamFrame{Type: "shell", Page: payload}
			if page.deferredJob != nil {
				frame.MetadataPending = true
				frame.MetadataOwner = page.metadataOwner
				frame.MetadataIndices = append([]int{}, page.metadataIndices...)
			}
			return write(frame)
		}
		if event.Err != nil {
			a.config.Logger.Printf("deferred boundary %s: %v", event.ID, event.Err)
		}
		return write(navigation.StreamFrame{Type: "chunk", ID: event.ID, HTML: string(event.HTML), Failed: event.Err != nil})
	}
	var err error
	if page.deferredJob == nil {
		err = ui.RenderEvents(ctx, page.Body, emitBody)
	} else {
		w.Header().Set(navigation.MetadataHeader, "1")
		err = renderMetadataEvents(ctx, page, emitBody, func(result metadataResult) error {
			if result.err != nil {
				a.config.Logger.Printf("deferred metadata failed (%T)", result.err)
			}
			data, e := metadataTerminal(result, page.metadataOwner, result.err != nil)
			if e != nil {
				return e
			}
			return writeEncoded(data, true)
		})
	}
	if err == nil {
		err = write(navigation.StreamFrame{Type: "done"})
	}
	if err != nil {
		a.config.Logger.Printf("navigation stream: %v", err)
		if !committed {
			w.Header().Del(navigation.StreamHeader)
			w.Header().Del(navigation.MetadataHeader)
			a.respond(w, r, errorPage(), 500)
		}
	}
}
