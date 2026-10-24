package seo

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

type ThemeColor struct{ Color, Media string }

// Viewport replaces an inherited viewport as one unit. Zero/empty fields use
// browser-friendly defaults. UserScalable nil keeps browser zoom available.
type Viewport struct {
	Width, Height                            string
	InitialScale, MinimumScale, MaximumScale float64
	UserScalable                             *bool
	Fit, InteractiveWidget                   string
	ColorScheme                              string
	ThemeColors                              []ThemeColor
}

func dimension(value, device string) bool {
	if value == "" || value == device {
		return true
	}
	n, err := strconv.Atoi(value)
	return err == nil && n >= 1 && n <= 10000 && strconv.Itoa(n) == value
}
func printable(value string, limit int) bool {
	if len(value) > limit {
		return false
	}
	for _, r := range value {
		if r < 32 || r > 126 {
			return false
		}
	}
	return true
}
func (v Viewport) Content() (string, error) {
	if !dimension(v.Width, "device-width") || !dimension(v.Height, "device-height") {
		return "", fmt.Errorf("seo: invalid viewport dimensions")
	}
	for _, scale := range []float64{v.InitialScale, v.MinimumScale, v.MaximumScale} {
		if math.IsNaN(scale) || math.IsInf(scale, 0) || scale < 0 || scale > 10 {
			return "", fmt.Errorf("seo: invalid viewport scale")
		}
	}
	if v.MaximumScale > 0 && v.MinimumScale > v.MaximumScale {
		return "", fmt.Errorf("seo: viewport minimum exceeds maximum")
	}
	if v.Fit != "" && v.Fit != "auto" && v.Fit != "contain" && v.Fit != "cover" {
		return "", fmt.Errorf("seo: invalid viewport fit")
	}
	if v.InteractiveWidget != "" && v.InteractiveWidget != "resizes-visual" && v.InteractiveWidget != "resizes-content" && v.InteractiveWidget != "overlays-content" {
		return "", fmt.Errorf("seo: invalid interactive widget policy")
	}
	switch v.ColorScheme {
	case "", "normal", "light", "dark", "light dark", "dark light", "only light", "only dark":
	default:
		return "", fmt.Errorf("seo: invalid color scheme")
	}
	if len(v.ThemeColors) > 16 {
		return "", fmt.Errorf("seo: too many theme colors")
	}
	for _, color := range v.ThemeColors {
		if color.Color == "" || !printable(color.Color, 256) || !printable(color.Media, 1024) {
			return "", fmt.Errorf("seo: invalid theme color")
		}
	}
	width, scale := v.Width, v.InitialScale
	if width == "" {
		width = "device-width"
	}
	if scale == 0 {
		scale = 1
	}
	values := []string{"width=" + width, "initial-scale=" + strconv.FormatFloat(scale, 'f', -1, 64)}
	if v.Height != "" {
		values = append(values, "height="+v.Height)
	}
	if v.MinimumScale != 0 {
		values = append(values, "minimum-scale="+strconv.FormatFloat(v.MinimumScale, 'f', -1, 64))
	}
	if v.MaximumScale != 0 {
		values = append(values, "maximum-scale="+strconv.FormatFloat(v.MaximumScale, 'f', -1, 64))
	}
	if v.UserScalable != nil {
		value := "yes"
		if !*v.UserScalable {
			value = "no"
		}
		values = append(values, "user-scalable="+value)
	}
	if v.Fit != "" {
		values = append(values, "viewport-fit="+v.Fit)
	}
	if v.InteractiveWidget != "" {
		values = append(values, "interactive-widget="+v.InteractiveWidget)
	}
	return strings.Join(values, ","), nil
}

type DocumentAttributes = navigation.Document

func ResolveDocument(m Metadata) (DocumentAttributes, error) {
	d := navigation.Document{Language: m.Language, Direction: m.Direction, Viewport: navigation.DefaultViewport}
	if d.Language == "" {
		d.Language = "en"
	}
	if d.Direction == "" {
		d.Direction = "ltr"
	}
	if m.Viewport != nil {
		var err error
		d.Viewport, err = m.Viewport.Content()
		if err != nil {
			return d, err
		}
	}
	return d, d.Validate()
}

func ValidateTitleTemplate(value string) error {
	if value != "" && (len(value) > 2048 || strings.Count(value, "%s") != 1) {
		return fmt.Errorf("seo: title template requires exactly one %%s and at most 2 KiB")
	}
	return nil
}

// ResolveContribution merges one directory/page contribution and applies the
// nearest inherited title template to an explicitly supplied relative title.
// Merge itself remains a pure value merge without title formatting.
func ResolveContribution(parent, child Metadata) (Metadata, error) {
	if err := ValidateTitleTemplate(parent.TitleTemplate); err != nil {
		return Metadata{}, err
	}
	if err := ValidateTitleTemplate(child.TitleTemplate); err != nil {
		return Metadata{}, err
	}
	m := Merge(parent, child)
	if child.Title != "" && !child.AbsoluteTitle && parent.TitleTemplate != "" {
		m.Title = strings.Replace(parent.TitleTemplate, "%s", child.Title, 1)
	}
	return m, nil
}
