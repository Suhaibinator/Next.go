package nextgo

import (
	"bytes"
	"strings"

	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

// Freeze backing storage as well as containers. A short metadata substring may
// otherwise keep an entire request or CMS document alive beyond its byte weight.
func ownCachedPage(value cachedPage) cachedPage {
	value.Regions = ownRegionSpecs(value.Regions)
	value.HTML = strings.Clone(value.HTML)
	value.Page.Title = strings.Clone(value.Page.Title)
	value.Page.Description = strings.Clone(value.Page.Description)
	value.Page.Meta = ownStringMap(value.Page.Meta)
	if doc := value.Page.document; doc != nil {
		copy := *doc
		copy.Language = strings.Clone(copy.Language)
		copy.Direction = strings.Clone(copy.Direction)
		copy.Viewport = strings.Clone(copy.Viewport)
		value.Page.document = &copy
	}
	if head := value.Page.preparedHead; head != nil {
		owned := &renderedHead{markup: strings.Clone(head.markup), elements: make([]seo.Element, len(head.elements))}
		for i, element := range head.elements {
			owned.elements[i] = seo.Element{Tag: strings.Clone(element.Tag), Text: strings.Clone(element.Text), Attrs: ownStringMap(element.Attrs)}
		}
		value.Page.preparedHead = owned
	}
	return value
}
func ownRegionSpecs(regions []ui.RegionSpec) []ui.RegionSpec {
	if regions == nil {
		return nil
	}
	owned := make([]ui.RegionSpec, len(regions))
	for i, region := range regions {
		region.ID = strings.Clone(region.ID)
		region.Region = strings.Clone(region.Region)
		region.Props = bytes.Clone(region.Props)
		owned[i] = region
	}
	return owned
}
func ownStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	owned := make(map[string]string, len(values))
	for key, value := range values {
		owned[strings.Clone(key)] = strings.Clone(value)
	}
	return owned
}
