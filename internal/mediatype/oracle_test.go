package mediatype

import (
	"maps"
	"mime"
	"testing"
)

var mediaOracleSeeds = []string{
	"video/webm", `video/webm; codecs="vp9,opus"`, "audio/wav; charset=utf-8",
	"VIDEO/WEBM; title*=utf-8''caf%C3%A9", "audio/*; x=1; x=1",
	"audio/wav; x=1; x=2", "video/webm;\u00a0x=1", "video/webm;",
	`video/webm; title="C:\test\é"`, "audio/ogg; title*0*=utf-8''caf%C3; title*1*=%A9",
	"audio/ogg; title*0=hello; title*1=world", "audio/wav; bad*=x-unsupported''value",
	"video/webm; codecs=\"vp9\r\nheader\"", "video/webm; x=\x00", "audio/",
}

func compareSDKParser(t *testing.T, input string) {
	t.Helper()
	actual, params, err := ParseMediaType(input)
	expected, expectedParams, expectedErr := mime.ParseMediaType(input)
	if actual != expected || (params == nil) != (expectedParams == nil) || !maps.Equal(params, expectedParams) || (err == nil) != (expectedErr == nil) {
		t.Fatalf("parser differs from SDK for %q: (%q, %v, %v) versus (%q, %v, %v)", input, actual, params, err, expected, expectedParams, expectedErr)
	}
	if err != nil && err.Error() != expectedErr.Error() {
		t.Fatalf("parser error differs from SDK for %q: %v versus %v", input, err, expectedErr)
	}
}

func TestStandardLibraryOracle(t *testing.T) {
	for _, value := range parseMediaTypeTests {
		compareSDKParser(t, value.in)
	}
	for _, value := range badMediaTypeTests {
		compareSDKParser(t, value.in)
	}
	for _, input := range mediaOracleSeeds {
		compareSDKParser(t, input)
	}
}

func FuzzStandardLibraryOracle(f *testing.F) {
	for _, value := range parseMediaTypeTests {
		f.Add(value.in)
	}
	for _, value := range badMediaTypeTests {
		f.Add(value.in)
	}
	for _, input := range mediaOracleSeeds {
		f.Add(input)
	}
	f.Fuzz(compareSDKParser)
}
