package nextgo

import (
	"fmt"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
	"strings"
)

type renderedHead struct {
	elements []seo.Element
	markup   string
}
type frozenHeadResources struct {
	resources, jsonld []seo.Element
	boundedMetadata   bool
}

func cloneHeadElements(input []seo.Element) []seo.Element {
	output := make([]seo.Element, len(input))
	for i, e := range input {
		output[i] = seo.Element{Tag: strings.Clone(e.Tag), Text: strings.Clone(e.Text), Attrs: ownStringMap(e.Attrs)}
	}
	return output
}
func freezeHeadResources(page Page) (frozen frozenHeadResources, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("head resource panicked")
		}
	}()
	frozen.resources, err = assets.Elements(page.Resources...)
	if err != nil {
		return
	}
	if len(page.JSONLD) > 16 {
		return frozen, fmt.Errorf("too many structured-data nodes")
	}
	total := 0
	for _, data := range page.JSONLD {
		element, e := data.Element()
		if e != nil {
			return frozen, e
		}
		total += len(element.Text)
		if total > 512<<10 {
			return frozen, fmt.Errorf("structured data exceeds 512 KiB")
		}
		frozen.jsonld = append(frozen.jsonld, element)
	}
	frozen.resources = cloneHeadElements(frozen.resources)
	frozen.jsonld = cloneHeadElements(frozen.jsonld)
	return
}

// Provenance is recorded before combining typed SEO, resources, managed defaults
// and JSONLD. Late assembly never executes an application resource callback.
func assembleHead(page Page, deployment paths.Config, frozen frozenHeadResources, owner string) (head []seo.Element, markup string, indices []int, err error) {
	defer func() {
		if v := recover(); v != nil {
			head = nil
			markup = ""
			indices = nil
			err = fmt.Errorf("head construction panicked")
		}
	}()
	page, err = normalizePageMetadata(page)
	if err != nil {
		return
	}
	metadata := page.Metadata
	metadata.Title = ""
	metadata.Description = ""
	typed, e := seo.Elements(metadata)
	if e != nil {
		err = e
		return
	}
	for _, element := range typed {
		if element.Tag == "meta" {
			if element.Attrs["name"] == "viewport" {
				continue
			}
			if name := element.Attrs["name"]; name != "" {
				if _, overridden := page.Meta[strings.ToLower(name)]; overridden {
					continue
				}
			}
		}
		indices = append(indices, len(head))
		head = append(head, element)
	}
	head = append(head, cloneHeadElements(frozen.resources)...)
	managedStart := len(head)
	for _, element := range page.managedHead {
		indices = append(indices, len(head))
		head = appendManagedHead(head, []seo.Element{element})
	}
	if frozen.boundedMetadata && len(indices) > navigation.MaxMetadataElements {
		return nil, "", nil, fmt.Errorf("metadata element limit exceeded")
	}
	for i, element := range head {
		if href := element.Attrs["href"]; strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") {
			var resolved string
			if i >= managedStart {
				resolved = href
			} else if strings.HasPrefix(href, "/assets/") {
				resolved, err = deployment.AssetURL(strings.TrimPrefix(href, "/assets"))
			} else {
				resolved, err = deployment.URL(href)
			}
			if err != nil {
				return
			}
			element.Attrs["href"] = resolved
			head[i] = element
		}
	}
	head = append(head, cloneHeadElements(frozen.jsonld)...)
	owned := map[int]bool{}
	for _, i := range indices {
		owned[i] = true
	}
	var nodes []ui.Node
	for i, element := range head {
		if element.Tag == "script" {
			nodes = append(nodes, renderedHTML(`<script type="application/ld+json" data-nextgo-head="">`+element.Text+`</script>`))
			continue
		}
		attrs := ui.Attrs{"data-nextgo-head": ""}
		for k, v := range element.Attrs {
			attrs[k] = v
		}
		if owner != "" && owned[i] {
			attrs["data-nextgo-metadata-owned"] = owner
		}
		nodes = append(nodes, ui.El(element.Tag, attrs))
	}
	markup, err = ui.HTML(ui.Fragment(nodes...))
	return
}
func pageHead(page Page) ([]seo.Element, string, error) { return pageHeadPaths(page, paths.Config{}) }
func pageHeadPaths(page Page, deployment paths.Config) (head []seo.Element, markup string, err error) {
	page, err = normalizePageMetadata(page)
	if err != nil {
		return nil, "", err
	}
	if page.preparedHead != nil {
		return page.preparedHead.elements, page.preparedHead.markup, nil
	}
	frozen, err := freezeHeadResources(page)
	if err != nil {
		return nil, "", err
	}
	head, markup, _, err = assembleHead(page, deployment, frozen, "")
	return
}
