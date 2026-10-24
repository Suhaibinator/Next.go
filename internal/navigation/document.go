package navigation

import (
	"fmt"
	"strings"
)

const DefaultViewport = "width=device-width,initial-scale=1"

// Document is the versioned description of framework-owned document attributes.
// It contains no executable markup and is validated before navigation commits.
type Document struct {
	Language  string `json:"lang"`
	Direction string `json:"dir"`
	Viewport  string `json:"viewport"`
}

func ValidLanguage(value string) bool {
	if value == "" || len(value) > 255 {
		return false
	}
	parts := strings.Split(value, "-")
	for i, part := range parts {
		if len(part) == 0 || len(part) > 8 {
			return false
		}
		for _, r := range part {
			letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
			if !letter && (i == 0 || r < '0' || r > '9') {
				return false
			}
		}
	}
	first := strings.ToLower(parts[0])
	return len(first) >= 2 || (first == "x" || first == "i") && len(parts) > 1
}
func (d Document) Validate() error {
	if !ValidLanguage(d.Language) {
		return fmt.Errorf("invalid document language")
	}
	if d.Direction != "ltr" && d.Direction != "rtl" && d.Direction != "auto" {
		return fmt.Errorf("invalid document direction")
	}
	if d.Viewport == "" || len(d.Viewport) > 2048 {
		return fmt.Errorf("invalid document viewport")
	}
	for _, r := range d.Viewport {
		if r < 32 || r > 126 {
			return fmt.Errorf("invalid document viewport")
		}
	}
	return nil
}
