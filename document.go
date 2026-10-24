package nextgo

import "github.com/Suhaibinator/Next.go/seo"

// Framework-owned styles are shared by buffered and streamed documents.
const frameworkCSS = `nextgo-layout,nextgo-outlet,nextgo-slot{display:contents}.nextgo-sr-only{position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0}body[data-nextgo-frozen] #nextgo-app,body[data-nextgo-frozen] #nextgo-app *{visibility:hidden!important;pointer-events:none!important}body[data-nextgo-frozen] #nextgo-status,body[data-nextgo-frozen] #nextgo-status *{visibility:visible!important;pointer-events:auto!important}`
const routeAnnouncer = `<div id="nextgo-announcer" class="nextgo-sr-only" role="status" aria-live="polite" aria-atomic="true"></div>`

func pageDocument(p Page) (Document, error) {
	if p.document != nil {
		return *p.document, p.document.Validate()
	}
	return seo.ResolveDocument(p.Metadata)
}
