package assets

import (
	"fmt"
	"github.com/Suhaibinator/Next.go/seo"
)

// Elements validates and snapshots all resources, including implementations
// supplied by an application. Resource elements are limited to local font
// preloads and stylesheet links; executable markup is never accepted.
func Elements(resources ...Resource) ([]seo.Element, error) {
	if len(resources) > 128 {
		return nil, fmt.Errorf("assets: too many resources")
	}
	var out []seo.Element
	for _, resource := range resources {
		if resource == nil {
			return nil, fmt.Errorf("assets: nil resource")
		}
		elements, err := resource.Elements()
		if err != nil {
			return nil, err
		}
		if len(out)+len(elements) > 256 {
			return nil, fmt.Errorf("assets: too many resource elements")
		}
		for _, element := range elements {
			if element.Tag != "link" || element.Text != "" {
				return nil, fmt.Errorf("assets: resources must be link elements")
			}
			attrs := element.Attrs
			if err := localURL(attrs["href"]); err != nil {
				return nil, err
			}
			allowed := map[string]bool{"rel": true, "href": true}
			switch attrs["rel"] {
			case "stylesheet":
				allowed["integrity"] = true
				allowed["crossorigin"] = true
				if integrity, ok := attrs["integrity"]; ok {
					if err := validIntegrity(integrity); err != nil {
						return nil, err
					}
					if attrs["crossorigin"] != "anonymous" {
						return nil, fmt.Errorf("assets: integrity requires anonymous CORS")
					}
				}
				if value, ok := attrs["crossorigin"]; ok && value != "anonymous" {
					return nil, fmt.Errorf("assets: invalid CORS mode")
				}
			case "preload":
				allowed["as"] = true
				allowed["type"] = true
				allowed["crossorigin"] = true
				if attrs["as"] != "font" || (attrs["type"] != "font/woff" && attrs["type"] != "font/woff2") || attrs["crossorigin"] != "anonymous" {
					return nil, fmt.Errorf("assets: invalid font preload")
				}
			default:
				return nil, fmt.Errorf("assets: invalid resource relation")
			}
			copied := make(map[string]string, len(attrs))
			for name, value := range attrs {
				if !allowed[name] {
					return nil, fmt.Errorf("assets: invalid resource attribute %q", name)
				}
				copied[name] = value
			}
			out = append(out, seo.Element{Tag: "link", Attrs: copied})
		}
	}
	return out, nil
}
