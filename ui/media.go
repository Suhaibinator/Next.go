package ui

import (
	"fmt"
	"strings"
)

type MediaPreload string

const (
	PreloadNone     MediaPreload = "none"
	PreloadMetadata MediaPreload = "metadata"
	PreloadAuto     MediaPreload = "auto"
)

type MediaSource struct {
	Src  string
	Type string // Optional MIME type, e.g. video/webm or audio/wav.
}

type TextTrack struct {
	Src, Kind, Language, Label string
	Default                    bool
}

// MediaOptions configures native controls. Label is the accessible player name;
// captions/subtitles need their own language and label. URLs are public browser
// resources, never fetched or transcoded by the server helpers.
type MediaOptions struct {
	Sources               []MediaSource
	Tracks                []TextTrack
	Label, ID, Class      string
	Preload               MediaPreload // Defaults to metadata.
	Muted, Autoplay, Loop bool
	CrossOrigin           string // Empty, anonymous or use-credentials.
	// Fallback is content for browsers without native audio/video support.
	Fallback Node
}

type VideoOptions struct {
	MediaOptions
	Poster        string
	Width, Height int
}

// Video renders escaped video with controls and playsinline. Muted must be set
// explicitly for autoplay. Browser codec, CORS, autoplay and CSP rules apply.
func Video(options VideoOptions) Node {
	if options.Width < 0 || options.Height < 0 {
		return resourceError(fmt.Errorf("ui: video dimensions must be nonnegative"))
	}
	attrs := Attrs{"playsinline": ""}
	resourceDimensions(attrs, options.Width, options.Height)
	if options.Poster != "" {
		if _, err := resourceURL(options.Poster); err != nil {
			return resourceError(err)
		}
		attrs["poster"] = options.Poster
	}
	return mediaElement("video", options.MediaOptions, attrs)
}

// Audio renders escaped audio with native controls and an accessible name.
func Audio(options MediaOptions) Node { return mediaElement("audio", options, Attrs{}) }

func mediaElement(tag string, options MediaOptions, attrs Attrs) Node {
	if strings.TrimSpace(options.Label) == "" || len(options.Sources) == 0 || len(options.Sources) > 16 || len(options.Tracks) > 32 {
		return resourceError(fmt.Errorf("ui: media requires a label and 1..16 sources, with at most 32 tracks"))
	}
	if options.Autoplay && !options.Muted {
		return resourceError(fmt.Errorf("ui: autoplay requires explicit muted playback"))
	}
	preload := options.Preload
	if preload == "" {
		preload = PreloadMetadata
	}
	if preload != PreloadNone && preload != PreloadMetadata && preload != PreloadAuto {
		return resourceError(fmt.Errorf("ui: invalid media preload"))
	}
	if options.CrossOrigin != "" && options.CrossOrigin != "anonymous" && options.CrossOrigin != "use-credentials" {
		return resourceError(fmt.Errorf("ui: invalid media crossorigin"))
	}
	attrs["controls"], attrs["aria-label"], attrs["preload"] = "", options.Label, string(preload)
	resourceIdentity(attrs, options.ID, options.Class)
	if options.CrossOrigin != "" {
		attrs["crossorigin"] = options.CrossOrigin
	}
	for _, flag := range []struct {
		enabled bool
		name    string
	}{{options.Muted, "muted"}, {options.Autoplay, "autoplay"}, {options.Loop, "loop"}} {
		if flag.enabled {
			attrs[flag.name] = ""
		}
	}
	var children []Node
	for _, source := range options.Sources {
		if _, err := resourceURL(source.Src); err != nil {
			return resourceError(err)
		}
		sourceAttrs := Attrs{"src": source.Src}
		if source.Type != "" {
			mediaType, err := parseMediaType(source.Type)
			if err != nil || !strings.HasPrefix(mediaType, tag+"/") {
				return resourceError(fmt.Errorf("ui: source MIME type must match %s", tag))
			}
			sourceAttrs["type"] = source.Type
		}
		children = append(children, El("source", sourceAttrs))
	}
	defaults := 0
	for _, track := range options.Tracks {
		if _, err := resourceURL(track.Src); err != nil {
			return resourceError(err)
		}
		kind := track.Kind
		if kind == "" {
			kind = "captions"
		}
		switch kind {
		case "captions", "subtitles":
			if strings.TrimSpace(track.Language) == "" || strings.TrimSpace(track.Label) == "" {
				return resourceError(fmt.Errorf("ui: captions and subtitles require language and label"))
			}
		case "descriptions", "chapters", "metadata":
		default:
			return resourceError(fmt.Errorf("ui: invalid media text track kind"))
		}
		trackAttrs := Attrs{"src": track.Src, "kind": kind}
		if track.Language != "" {
			trackAttrs["srclang"] = track.Language
		}
		if track.Label != "" {
			trackAttrs["label"] = track.Label
		}
		if track.Default {
			defaults++
			trackAttrs["default"] = ""
		}
		children = append(children, El("track", trackAttrs))
	}
	if defaults > 1 {
		return resourceError(fmt.Errorf("ui: media allows only one default text track"))
	}
	fallback := options.Fallback
	if fallback == nil {
		fallback = El("a", Attrs{"href": options.Sources[0].Src, "download": ""}, Text("Download "+options.Label))
	}
	children = append(children, fallback)
	return El(tag, attrs, children...)
}
