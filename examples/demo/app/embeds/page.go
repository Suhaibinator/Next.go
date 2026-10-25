//go:build !js

package embeds

import (
	"net/url"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	// The local frame and optimized poster need live API/image endpoints.
	if err := c.RequireServer(); err != nil {
		return nextgo.Page{}, err
	}
	policy, err := ui.NewFramePolicy(ui.FramePolicyOptions{AllowSelf: true})
	if err != nil {
		return nextgo.Page{}, err
	}
	frameURL, err := policy.URL("", nextgo.MustURL(c, "/api/embed"), url.Values{"message": {"A local document, isolated by the browser sandbox."}})
	if err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{
		Metadata: seo.Metadata{Title: "Safe embeds — Next.go", AbsoluteTitle: true, Description: "Typed sandboxed frames and native accessible media controls, rendered by Go."},
		Body: ui.El("article", ui.Attrs{"class": "prose"},
			ui.El("p", ui.Attrs{"class": "eyebrow accent"}, ui.Text("SANDBOXED FRAMES / NATIVE MEDIA")),
			ui.El("h1", nil, ui.Text("Explicit trust.\nNative controls.")),
			ui.El("p", ui.Attrs{"class": "lead"}, ui.Text("This local frame has no scripts, no same-origin grant, no referrer and no camera or microphone permissions. The fallback link stays available.")),
			policy.Frame(ui.FrameOptions{Src: frameURL, ID: "safe-frame", Title: "Local sandboxed document", Width: 640, Height: 180, Eager: true}),
			ui.El("h2", nil, ui.Text("Video with captions")),
			ui.Video(ui.VideoOptions{
				MediaOptions: ui.MediaOptions{ID: "native-video", Label: "Two seconds of teal", Preload: ui.PreloadMetadata,
					Sources: []ui.MediaSource{{Src: nextgo.MustAssetURL(c, "/embeds-demo.webm"), Type: "video/webm"}, {Src: nextgo.MustAssetURL(c, "/embeds-demo.mp4"), Type: "video/mp4"}},
					Tracks:  []ui.TextTrack{{Src: nextgo.MustAssetURL(c, "/embeds-demo.vtt"), Language: "en", Label: "English captions", Default: true}}},
				Poster: nextgo.MustURL(c, "/_nextgo/image?src=%2Fdemo.png&w=320&q=75"), Width: 320, Height: 180,
			}),
			ui.El("p", nil, ui.Text("Transcript: a silent teal screen with an orange rectangle. Captions describe this original two-second clip.")),
			ui.El("h2", nil, ui.Text("A short audio tone")),
			ui.Audio(ui.MediaOptions{ID: "native-audio", Label: "Short 440 hertz tone", Preload: ui.PreloadMetadata, Sources: []ui.MediaSource{{Src: nextgo.MustAssetURL(c, "/embeds-demo.wav"), Type: "audio/wav"}}}),
			ui.El("p", nil, ui.Text("Audio description: a quiet, short 440 hertz tone. Playback starts only when you use the native controls.")),
			ui.El("p", nil, ui.Text("Frame policies validate initial destinations. An application CSP frame-src directive must also restrict redirects and later frame navigation.")),
			ui.Link(nextgo.MustURL(c, "/media"), ui.LinkOptions{}, ui.Text("Responsive images →")),
			ui.El("p", nil, ui.Link(nextgo.MustURL(c, "/"), ui.LinkOptions{}, ui.Text("Back to home")))),
	}, nil
}
