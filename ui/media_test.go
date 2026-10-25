package ui

import (
	"strings"
	"testing"
)

func TestNativeMediaDefaultsSourcesCaptionsAndEscaping(t *testing.T) {
	got, err := HTML(Video(VideoOptions{MediaOptions: MediaOptions{Label: `A "safe" <video>`, ID: "film", Sources: []MediaSource{{Src: "/film.webm?a=1&b=2", Type: `video/webm; codecs="vp9"`}, {Src: "https://media.test/film.mp4", Type: "video/mp4"}}, Tracks: []TextTrack{{Src: "/captions.vtt", Language: "en", Label: `English <captions>`, Default: true}}}, Poster: "/poster.png", Width: 640, Height: 360}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<video `, `controls=""`, `preload="metadata"`, `playsinline=""`, `poster="/poster.png"`, `width="640"`, `height="360"`, `aria-label="A &#34;safe&#34; &lt;video&gt;"`, `src="/film.webm?a=1&amp;b=2"`, `type="video/webm; codecs=&#34;vp9&#34;"`, `kind="captions"`, `srclang="en"`, `label="English &lt;captions&gt;"`, `default=""`, "Download A &#34;safe&#34; &lt;video&gt;"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "<captions>") || strings.Contains(got, `autoplay=`) {
		t.Fatal(got)
	}
	got, err = HTML(Audio(MediaOptions{Label: "Tone", Sources: []MediaSource{{Src: "/tone.wav", Type: "audio/wav"}}, Preload: PreloadNone, Muted: true, Autoplay: true, Loop: true, CrossOrigin: "anonymous", Fallback: Text("Read the transcript")}))
	if err != nil || !strings.Contains(got, `<audio `) || !strings.Contains(got, `preload="none"`) || !strings.Contains(got, `muted=""`) || !strings.Contains(got, `autoplay=""`) || !strings.Contains(got, `crossorigin="anonymous"`) || !strings.HasSuffix(got, "Read the transcript</audio>") {
		t.Fatal(got, err)
	}
}

func TestNativeMediaRejectsUnsafeOptionsBeforeOutput(t *testing.T) {
	valid := func() MediaOptions {
		return MediaOptions{Label: "Audio", Sources: []MediaSource{{Src: "/audio.wav", Type: "audio/wav"}}}
	}
	var cases []MediaOptions
	for _, src := range []string{"javascript:alert(1)", "data:audio/wav,AAAA", "blob:https://media.test/id", "//media.test/a.wav", "https://user:secret@media.test/a.wav", "https://media.test\\@evil.test/a.wav", "relative.wav", "/audio\n.wav"} {
		o := valid()
		o.Sources[0].Src = src
		cases = append(cases, o)
	}
	for _, mutate := range []func(*MediaOptions){func(o *MediaOptions) { o.Label = " " }, func(o *MediaOptions) { o.Sources = nil }, func(o *MediaOptions) { o.Sources[0].Type = "video/webm" }, func(o *MediaOptions) { o.Preload = "everything" }, func(o *MediaOptions) { o.CrossOrigin = "unsafe" }, func(o *MediaOptions) { o.Autoplay = true }, func(o *MediaOptions) { o.Tracks = []TextTrack{{Src: "/c.vtt", Kind: "script"}} }, func(o *MediaOptions) { o.Tracks = []TextTrack{{Src: "/c.vtt", Kind: "captions"}} }, func(o *MediaOptions) {
		o.Tracks = []TextTrack{{Src: "javascript:alert(1)", Language: "en", Label: "English"}}
	}, func(o *MediaOptions) {
		o.Tracks = []TextTrack{{Src: "/a.vtt", Kind: "metadata", Default: true}, {Src: "/b.vtt", Kind: "metadata", Default: true}}
	}} {
		o := valid()
		mutate(&o)
		cases = append(cases, o)
	}
	for _, o := range cases {
		if got, err := HTML(Audio(o)); err == nil || got != "" {
			t.Errorf("invalid media partially rendered: %q %v", got, err)
		}
	}
	for _, o := range []VideoOptions{{MediaOptions: MediaOptions{Label: "Video", Sources: []MediaSource{{Src: "/video.webm"}}}, Poster: "javascript:alert(1)"}, {MediaOptions: MediaOptions{Label: "Video", Sources: []MediaSource{{Src: "/video.webm"}}}, Width: -1}} {
		if got, err := HTML(Video(o)); err == nil || got != "" {
			t.Fatal(got, err)
		}
	}
}
