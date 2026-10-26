//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

// metadataHead records provenance as exact node identities. Tags and relations
// cannot establish ownership. Preserve the exact provenance supplied by the server.
type metadataHead struct {
	owner              string
	indices            []int
	nodes              []js.Value
	title, description js.Value
	active             func() bool
	completed          bool
	committedURL       string
}

func metadataElements(elements []headElement) []navigation.MetadataElement {
	out := make([]navigation.MetadataElement, len(elements))
	for i, e := range elements {
		out[i] = navigation.MetadataElement{Tag: e.Tag, Attrs: e.Attrs, Text: e.Text}
	}
	return out
}
func metadataHeadElements(elements []navigation.MetadataElement) []headElement {
	out := make([]headElement, len(elements))
	for i, e := range elements {
		out[i] = headElement{Tag: e.Tag, Attrs: e.Attrs, Text: e.Text}
	}
	return out
}

func newNavigationMetadata(ctx context.Context, id uint64, owner string, indices []int, data envelope) (*metadataHead, error) {
	if !navigation.ValidMetadataOwner(owner) {
		return nil, fmt.Errorf("invalid metadata owner")
	}
	if err := navigation.ValidateMetadataIndices(indices, len(data.Head)); err != nil {
		return nil, err
	}
	selected := make([]navigation.MetadataElement, 0, len(indices))
	for _, index := range indices {
		selected = append(selected, metadataElements(data.Head[index : index+1])[0])
	}
	if _, err := navigation.EncodeMetadataPayload(navigation.MetadataPayload{Version: 1, Owner: owner, Title: data.Title, Description: data.Description, Elements: selected}, false); err != nil {
		return nil, err
	}
	p := &metadataHead{owner: owner, indices: append([]int(nil), indices...)}
	p.active = func() bool {
		return ctx.Err() == nil && id == navigationID && !draftFrozen && !refreshFrozen() && p.committedURL != "" && p.committedURL == currentURL && documentConfiguration() == data.Configuration && doc.Get("body").Call("getAttribute", "data-nextgo-build").String() == data.Build
	}
	return p, nil
}

func (p *metadataHead) capture(fragment js.Value) error {
	head := doc.Get("head")
	titles := head.Call("querySelectorAll", "title")
	descriptions := head.Call("querySelectorAll", `meta[name="description"]`)
	if titles.Length() != 1 || descriptions.Length() != 1 {
		return fmt.Errorf("invalid framework metadata ownership")
	}
	p.title, p.description = titles.Index(0), descriptions.Index(0)
	children := fragment.Get("childNodes")
	if err := navigation.ValidateMetadataIndices(p.indices, children.Length()); err != nil {
		return err
	}
	p.nodes = make([]js.Value, 0, len(p.indices))
	for _, index := range p.indices {
		node := children.Index(index)
		node.Call("setAttribute", "data-nextgo-metadata-owned", p.owner)
		p.nodes = append(p.nodes, node)
	}
	return nil
}

func (p *metadataHead) ownsHead() bool {
	if p == nil || p.active == nil || !p.active() || p.completed {
		return false
	}
	head := doc.Get("head")
	titles := head.Call("querySelectorAll", "title")
	descriptions := head.Call("querySelectorAll", `meta[name="description"]`)
	if titles.Length() != 1 || descriptions.Length() != 1 || !titles.Index(0).Equal(p.title) || !descriptions.Index(0).Equal(p.description) || !p.title.Get("parentNode").Equal(head) || !p.description.Get("parentNode").Equal(head) {
		return false
	}
	for _, node := range p.nodes {
		if !node.Get("parentNode").Equal(head) || node.Call("getAttribute", "data-nextgo-metadata-owned").String() != p.owner {
			return false
		}
	}
	return true
}

// apply validates every descriptor and replacement node before any mutation.
// It never calls navigation commit, hydrates islands, or changes history/focus.
func (p *metadataHead) apply(frame navigation.StreamFrame) error {
	if !p.ownsHead() {
		return context.Canceled
	}
	encoded, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if _, err = navigation.DecodeMetadataStreamFrame(encoded); err != nil {
		return err
	}
	payload, err := navigation.DecodeMetadataPayload(frame.Metadata, frame.Failed)
	if err != nil {
		return err
	}
	if frame.Type != "metadata" || payload.Owner != p.owner {
		return fmt.Errorf("metadata completion owner mismatch")
	}
	if frame.Failed {
		p.completed = true
		return nil
	}
	fragment, err := buildHead(metadataHeadElements(payload.Elements))
	if err != nil {
		return err
	}
	children := fragment.Get("childNodes")
	replacement := make([]js.Value, children.Length())
	for i := range replacement {
		replacement[i] = children.Index(i)
		replacement[i].Call("setAttribute", "data-nextgo-metadata-owned", p.owner)
	}
	// DOM/URL preparation can invoke browser hooks. Recheck generation and all
	// retained ownership after preparation, immediately before the transaction.
	if !p.ownsHead() {
		return context.Canceled
	}
	anchor := p.description.Get("nextSibling")
	if len(p.nodes) > 0 {
		anchor = p.nodes[0]
	}
	doc.Get("head").Call("insertBefore", fragment, anchor)
	p.title.Set("textContent", payload.Title)
	p.description.Call("setAttribute", "content", payload.Description)
	for _, node := range p.nodes {
		node.Call("remove")
	}
	p.nodes = replacement
	p.completed = true
	return nil
}

var initialMetadata *metadataHead
var initialMetadataGeneration uint64

func startInitialMetadata() error {
	initialMetadataGeneration++
	initialMetadata = nil
	body := doc.Get("body")
	version := body.Call("getAttribute", "data-nextgo-metadata-version")
	owner := body.Call("getAttribute", "data-nextgo-metadata-owner")
	if version.IsNull() && owner.IsNull() {
		return nil
	}
	if version.IsNull() || version.String() != strconv.Itoa(navigation.MetadataVersion) || owner.IsNull() || !navigation.ValidMetadataOwner(owner.String()) {
		return fmt.Errorf("invalid initial metadata declaration")
	}
	token := owner.String()
	generation := initialMetadataGeneration
	navigationGeneration := navigationID
	build := body.Call("getAttribute", "data-nextgo-build").String()
	configuration := documentConfiguration()
	p := &metadataHead{owner: token}
	p.active = func() bool {
		return streamingActive && navigationID == navigationGeneration && initialMetadataGeneration == generation && initialMetadata == p && !draftFrozen && !refreshFrozen() && documentConfiguration() == configuration && body.Equal(doc.Get("body")) && body.Call("getAttribute", "data-nextgo-build").String() == build
	}
	marked := doc.Get("head").Call("querySelectorAll", "[data-nextgo-metadata-owned]")
	if marked.Length() > navigation.MaxMetadataElements {
		return fmt.Errorf("too many initial metadata nodes")
	}
	elements := make([]navigation.MetadataElement, 0, marked.Length())
	p.nodes = make([]js.Value, 0, marked.Length())
	for i := 0; i < marked.Length(); i++ {
		node := marked.Index(i)
		if !node.Get("parentNode").Equal(doc.Get("head")) || node.Call("getAttribute", "data-nextgo-metadata-owned").String() != token {
			return fmt.Errorf("invalid initial metadata ownership")
		}
		element := navigation.MetadataElement{Tag: node.Get("tagName").String(), Attrs: map[string]string{}}
		switch element.Tag {
		case "META":
			element.Tag = "meta"
		case "LINK":
			element.Tag = "link"
		default:
			return fmt.Errorf("invalid initial metadata node")
		}
		attrs := node.Get("attributes")
		for j := 0; j < attrs.Length(); j++ {
			attr := attrs.Index(j)
			name := attr.Get("name").String()
			if name != "data-nextgo-head" && name != "data-nextgo-metadata-owned" {
				element.Attrs[name] = attr.Get("value").String()
			}
		}
		elements = append(elements, element)
		p.nodes = append(p.nodes, node)
	}
	if _, err := navigation.EncodeMetadataPayload(navigation.MetadataPayload{Version: 1, Owner: token, Elements: elements}, false); err != nil {
		return err
	}
	head := doc.Get("head")
	titles := head.Call("querySelectorAll", "title")
	descriptions := head.Call("querySelectorAll", `meta[name="description"]`)
	if titles.Length() != 1 || descriptions.Length() != 1 {
		return fmt.Errorf("invalid framework metadata ownership")
	}
	p.title, p.description = titles.Index(0), descriptions.Index(0)
	initialMetadata = p
	return nil
}

func stopInitialMetadata() { initialMetadataGeneration++; initialMetadata = nil }

func completedMetadataTemplate(template js.Value) (js.Value, bool) {
	token := template.Call("getAttribute", "data-nextgo-metadata").String()
	marker := template.Get("nextSibling")
	if !navigation.ValidMetadataOwner(token) || marker.IsNull() || marker.IsUndefined() || marker.Get("nodeType").Int() != 1 || marker.Get("tagName").String() != "SPAN" || !marker.Call("hasAttribute", "hidden").Bool() {
		return js.Undefined(), false
	}
	ready := marker.Call("getAttribute", "data-nextgo-metadata-ready")
	if ready.IsNull() || ready.String() != token {
		return js.Undefined(), false
	}
	return marker, true
}

// Bound UTF-16 length before copying into Go, then enforce the UTF-8 byte cap.
// syscall/js.Length/Get require objects, so box the primitive browser string.
func metadataCarrierText(text js.Value) (string, error) {
	if text.Type() != js.TypeString || js.Global().Get("Object").Invoke(text).Get("length").Int() > navigation.MaxMetadataBytes {
		return "", fmt.Errorf("metadata carrier exceeds limits")
	}
	raw := text.String()
	if len(raw) > navigation.MaxMetadataBytes {
		return "", fmt.Errorf("metadata carrier exceeds limits")
	}
	return raw, nil
}

func applyMetadataCarriers() {
	templates := doc.Call("querySelectorAll", "template[data-nextgo-metadata]")
	for i := 0; i < templates.Length(); i++ {
		template := templates.Index(i)
		marker, complete := completedMetadataTemplate(template)
		p := initialMetadata
		if p != nil && !p.active() {
			p.completed = true
			template.Call("remove")
			if complete {
				marker.Call("remove")
			}
			continue
		}
		if !streamingActive || p == nil || p.completed || templates.Length() > 1 {
			if p != nil && p.completed && p.active() {
				report("Invalid duplicate page metadata completion.")
			}
			template.Call("remove")
			if complete {
				marker.Call("remove")
			}
			if p != nil && templates.Length() > 1 {
				p.completed = true
				report("Invalid page metadata completion.")
			}
			continue
		}
		if !complete {
			continue
		}
		raw, textErr := metadataCarrierText(template.Get("content").Get("textContent"))
		template.Call("remove")
		marker.Call("remove")
		if textErr != nil {
			p.completed = true
			report("Invalid page metadata completion.")
			continue
		}

		frame, err := navigation.DecodeMetadataStreamFrame([]byte(raw))
		if err == nil && frame.Type == "metadata" && template.Call("getAttribute", "data-nextgo-metadata").String() == p.owner {
			err = p.apply(frame)
		} else if err == nil {
			err = fmt.Errorf("invalid metadata completion")
		}
		if err != nil {
			p.completed = true
			if p.active() {
				report("Unable to load page metadata.")
			}
		}
	}
	if !streamingActive || initialMetadata == nil || initialMetadata.completed {
		markers := doc.Call("querySelectorAll", "[data-nextgo-metadata-ready]")
		for i := 0; i < markers.Length(); i++ {
			markers.Index(i).Call("remove")
		}
	}
}
