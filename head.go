package nextgo

import (
	"fmt"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
	"strings"
)

type renderedHead struct {
	elements []seo.Element
	markup   string
}

// Validate all head contributions before writing or caching a response.
func pageHead(page Page) ([]seo.Element, string, error) { return pageHeadPaths(page, paths.Config{}) }
func pageHeadPaths(page Page, deployment paths.Config) (head []seo.Element, markup string, err error) {
	page, err = normalizePageMetadata(page)
	if err != nil {
		return nil, "", err
	}
	if page.preparedHead != nil {
		return page.preparedHead.elements, page.preparedHead.markup, nil
	}
	defer func() {
		if cause := recover(); cause != nil {
			head = nil
			markup = ""
			err = fmt.Errorf("head resource panic: %v", cause)
		}
	}()
	metadata := page.Metadata
	metadata.Title = ""
	metadata.Description = ""
	head, err = seo.Elements(metadata)
	if err != nil {
		return nil, "", err
	}
	resources, err := assets.Elements(page.Resources...)
	if err != nil {
		return nil, "", err
	}
	filtered := head[:0]
	for _, element := range head {
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
		filtered = append(filtered, element)
	}
	head = append(filtered, resources...)
	nodes := make([]ui.Node, 0, len(head))
	for index, element := range head {
		if href := element.Attrs["href"]; strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") {
			var resolved string
			var resolveErr error
			if strings.HasPrefix(href, "/assets/") {
				resolved, resolveErr = deployment.AssetURL(strings.TrimPrefix(href, "/assets"))
			} else {
				resolved, resolveErr = deployment.URL(href)
			}
			if resolveErr != nil {
				return nil, "", resolveErr
			}
			element.Attrs["href"] = resolved
			head[index] = element
		}
		attrs := ui.Attrs{"data-nextgo-head": ""}
		for key, value := range element.Attrs {
			attrs[key] = value
		}
		nodes = append(nodes, ui.El(element.Tag, attrs))
	}
	// These links already contain the final global BasePath. They must not pass
	// through the normal locale/asset resolver above a second time.
	beforeManaged := len(head)
	head = appendManagedHead(head, page.managedHead)
	for _, element := range head[beforeManaged:] {
		attrs := ui.Attrs{"data-nextgo-head": ""}
		for key, value := range element.Attrs {
			attrs[key] = value
		}
		nodes = append(nodes, ui.El(element.Tag, attrs))
	}
	if len(page.JSONLD) > 16 {
		return nil, "", fmt.Errorf("too many structured-data nodes")
	}
	totalJSON := 0
	for _, data := range page.JSONLD {
		element, err := data.Element()
		if err != nil {
			return nil, "", err
		}
		totalJSON += len(element.Text)
		if totalJSON > 512<<10 {
			return nil, "", fmt.Errorf("structured data exceeds 512 KiB")
		}
		head = append(head, element)
		nodes = append(nodes, data.Node())
	}
	markup, err = ui.HTML(ui.Fragment(nodes...))
	return head, markup, err
}
