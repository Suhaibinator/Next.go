//go:build !js

package fileform

import (
	"github.com/Suhaibinator/Next.go/examples/demo/uploads"
	"github.com/Suhaibinator/Next.go/ui"
)

func View(p Props) ui.Node {
	id := func(name string) string { return p.Prefix + "-" + name }
	tag := "form"
	attrs := ui.Attrs{"method": "post", "enctype": "multipart/form-data", "action": p.Action, "data-file-form": ""}
	apply := ui.Attrs{"type": "submit", "data-file-apply": ""}
	reset := ui.Attrs{"type": "reset", "data-file-reset": ""}
	label := "Receive files"
	description := "Small public demo uploads: one or two files, up to 256 KiB each. Bytes are hashed and discarded; receipt metadata is public and process-local, not durable or authenticated storage."
	var native ui.Node = ui.Fragment()
	if p.Local {
		tag = "section"
		attrs = ui.Attrs{"data-file-form": ""}
		apply["type"] = "button"
		apply["disabled"] = ""
		reset["type"] = "button"
		reset["disabled"] = ""
		label = "Read files locally"
		description = "A local Go file reader. Selected bytes stay in this browser and are not sent to a server."
	} else {
		native = ui.El("button", ui.Attrs{"type": "submit", "name": "intent", "value": "receive"}, ui.Text("Receive with native POST"))
	}
	return ui.Island("fileform", p, ui.El("section", ui.Attrs{"aria-label": "File editor"}, ui.El("h1", nil, ui.Text("Go file inputs")), ui.El("p", nil, ui.Text(description)),
		ui.El(tag, attrs,
			ui.El("label", ui.Attrs{"for": id("title")}, ui.Text("Upload label")), ui.El("input", ui.Attrs{"type": "text", "name": "title", "id": id("title"), "value": p.Title, "data-file-title": "", "aria-describedby": id("title-error")}), ui.El("span", ui.Attrs{"id": id("title-error"), "data-file-error": "title", "role": "alert"}),
			ui.El("label", ui.Attrs{"for": id("files")}, ui.Text("Files")), ui.El("input", ui.Attrs{"type": "file", "multiple": "", "name": "files", "id": id("files"), "data-files": "", "aria-describedby": id("files-error")}), ui.El("span", ui.Attrs{"id": id("files-error"), "data-file-error": "files", "role": "alert"}),
			ui.El("output", ui.Attrs{"data-file-selection": "", "aria-live": "polite"}, ui.Text("No files selected.")), ui.El("button", apply, ui.Text(label)), ui.El("button", reset, ui.Text("Reset to confirmed values")), ui.El("button", ui.Attrs{"type": "button", "data-file-cancel": "", "disabled": ""}, ui.Text("Cancel file work")), native,
			ui.El("p", ui.Attrs{"data-file-status": "", "role": "status"}, ui.Text("Ready.")), ui.El("pre", ui.Attrs{"data-file-receipt": ""}, ui.Text(uploads.Summary(p.Receipt)))), ui.El("a", ui.Attrs{"href": p.Target}, ui.Text("Leave file editor"))))
}
