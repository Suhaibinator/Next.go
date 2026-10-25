package ui

import (
	"mime"
	"strings"
	"testing"
)

// The same oracle runs on native and js/wasm targets. Importing mime here is
// deliberate test-only coverage; it does not enter the production WASM graph.
func TestMediaTypeHelperStandardLibraryOracle(t *testing.T) {
	for _, value := range []string{
		"video/webm", `VIDEO/WEBM; codecs="vp9,opus"`, "audio/wav", "audio/*",
		"audio/ogg; title*=utf-8''caf%C3%A9", "video/webm; x=1; x=1",
		"video/webm; x=1; x=2", "audio/ogg; x*0*=utf-8''caf%C3; x*1*=%A9",
		"audio/ogg;\u00a0x=1", `audio/ogg; title="C:\test\é"`, "video/webm;",
		"video/webm; broken", "text/html", "video/", "audio/ogg; x=\"bad\r\nvalue\"",
	} {
		expected, _, expectedErr := mime.ParseMediaType(value)
		actual, err := parseMediaType(value)
		if actual != expected || (err == nil) != (expectedErr == nil) {
			t.Fatalf("media parser differs from SDK for %q", value)
		}
		for _, tag := range []string{"video", "audio"} {
			options := MediaOptions{Label: "MIME oracle", Sources: []MediaSource{{Src: "/sample", Type: value}}}
			var node Node = Audio(options)
			if tag == "video" {
				node = Video(VideoOptions{MediaOptions: options})
			}
			_, renderErr := HTML(node)
			valid := expectedErr == nil && strings.HasPrefix(expected, tag+"/")
			if (renderErr == nil) != valid {
				t.Fatalf("%s MIME acceptance differs for %q: %v", tag, value, renderErr)
			}
		}
	}
}
