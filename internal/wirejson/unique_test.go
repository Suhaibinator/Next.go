package wirejson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"strings"
	"testing"
)

// decoderUnique is the previous implementation, kept test-only as the
// acceptance oracle across supported SDKs (including jsonv2's legacy API).
func decoderUnique(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return errors.New("JSON nesting exceeds 64")
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return errors.New("duplicate or invalid object key")
				}
				seen[name] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("unexpected JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func checkUniqueOracle(t testing.TB, data []byte) {
	t.Helper()
	got, want := Unique(data), decoderUnique(data)
	if (got == nil) != (want == nil) {
		t.Fatalf("Unique(%q) = %v; decoder acceptance oracle = %v", data, got, want)
	}
}

func TestUniqueContract(t *testing.T) {
	tests := []struct {
		name, input string
		valid       bool
	}{
		{"null", " \r\n\tnull ", true},
		{"boolean", "false", true},
		{"negative zero", "-0", true},
		{"unbounded number", "-123456789012345678901234567890.123e+99999999999999999", true},
		{"mixed", `{"values":[true,false,null,{},[],"text"],"x":-2.0E-12}`, true},
		{"nested key reuse", `{"x":1,"nested":{"x":2}}`, true},
		{"case sensitive", `{"x":1,"X":2}`, true},
		{"no unicode normalization", "{\"é\":1,\"é\":2}", true},
		{"empty string", `""`, true},
		{"escapes", `"\"\\\/\b\f\n\r\t\u0000"`, true},
		{"empty key", `{"":1}`, true},
		{"invalid UTF8 value", "\"\xff\xc0\xaf\xed\xa0\x80\"", true},
		{"lone surrogates", `"\ud800\udbff\udc00\udfff"`, true},
		{"replacement key", "{\"\xff\":1}", true},
		{"duplicate", `{"x":1,"x":2}`, false},
		{"duplicate escaped", `{"x":1,"\u0078":2}`, false},
		{"duplicate slash", `{"/":1,"\/":2}`, false},
		{"duplicate NUL", `{"\u0000":1,"\u0000":2}`, false},
		{"duplicate surrogate pair", "{\"😀\":1,\"\\ud83d\\ude00\":2}", false},
		{"duplicate high surrogate", `{"\ud800":1,"\ufffd":2}`, false},
		{"duplicate low surrogate", `{"\udfff":1,"\ufffd":2}`, false},
		{"duplicate invalid UTF8", "{\"\xff\":1,\"\\ufffd\":2}", false},
		{"invalid UTF8 per byte", "{\"\xed\xa0\x80\":1,\"\\ufffd\\ufffd\\ufffd\":2}", false},
		{"distinct malformed keys", "{\"\xff\":1,\"\xff\xff\":2}", true},
		{"surrogate replacement then pair", `{"\ud800\ud800\udc00":1,"\ufffd\ud800\udc00":2}`, false},
		{"empty", "", false},
		{"whitespace", " \n\t", false},
		{"BOM", "\xef\xbb\xbfnull", false},
		{"non JSON space", "\vnull", false},
		{"extra value", "true false", false},
		{"extra delimiter", "{}]", false},
		{"leading zero", "01", false},
		{"negative leading zero", "-01", false},
		{"plus", "+1", false},
		{"dot", ".1", false},
		{"missing fraction", "1.", false},
		{"missing exponent", "1e+", false},
		{"NaN", "NaN", false},
		{"infinity", "Infinity", false},
		{"incomplete literal", "nul", false},
		{"bad literal", "True", false},
		{"array trailing comma", "[1,]", false},
		{"object trailing comma", `{"x":1,}`, false},
		{"missing colon", `{"x" 1}`, false},
		{"missing comma", `{"x":1 "y":2}`, false},
		{"nonstring key", `{1:2}`, false},
		{"unescaped control", "\"x\x00y\"", false},
		{"bad escape", `"\x20"`, false},
		{"escaped apostrophe", `"\'"`, false},
		{"short unicode escape", `"\u123"`, false},
		{"bad unicode escape", `"\u12xz"`, false},
		{"bad second surrogate escape", `"\ud800\u12xz"`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Unique([]byte(tt.input)); (err == nil) != tt.valid {
				t.Fatalf("Unique(%q) = %v; want valid=%v", tt.input, err, tt.valid)
			}
			checkUniqueOracle(t, []byte(tt.input))
		})
	}
}

func TestUniqueDepthBoundary(t *testing.T) {
	for _, container := range []struct{ open, close string }{{"[", "]"}, {`{"x":`, "}"}} {
		for _, test := range []struct {
			count int
			leaf  string
			valid bool
		}{{64, "null", true}, {65, "null", false}, {64, "[]", true}, {65, "[]", false}, {64, "{}", true}, {65, "{}", false}} {
			data := []byte(strings.Repeat(container.open, test.count) + test.leaf + strings.Repeat(container.close, test.count))
			if err := Unique(data); (err == nil) != test.valid {
				t.Fatalf("depth=%d leaf=%s open=%s: %v; want valid=%v", test.count, test.leaf, container.open, err, test.valid)
			}
			checkUniqueOracle(t, data)
		}
	}
}

func uniqueSeeds() [][]byte {
	return [][]byte{
		[]byte(`{"version":1,"type":"shell","page":{"title":"Hello","html":"<p>Go</p>","meta":{"description":"text"}}}`),
		[]byte(`{"data":{"count":9223372036854775807,"items":[true,null,-1.25e+1000]},"redirect":""}`),
		[]byte(`{"\u0061":1,"a":2}`),
		[]byte(`{"\ud800":1,"\ufffd":2}`),
		[]byte("{\"\xff\":1,\"�\":2}"),
		[]byte(`"\ud800\ud800\udc00\udfff"`),
		[]byte(`"\"\\\/\b\f\n\r\t\u0000"`),
		[]byte("\"\xed\xa0\x80\xc0\xaf\xff\""),
		[]byte("-1234567890.123456789e-99999999"),
		[]byte("true"), []byte("false"), []byte("null"), []byte("{}"), []byte("[]"),
		[]byte(strings.Repeat("[", 64) + "0" + strings.Repeat("]", 64)),
		[]byte(strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65)),
	}
}

func TestUniqueDifferentialMutations(t *testing.T) {
	for _, seed := range uniqueSeeds() {
		checkUniqueOracle(t, seed)
		for i := 0; i <= len(seed); i++ {
			checkUniqueOracle(t, seed[:i])
		}
	}
	// Mutate valid structural inputs rather than mostly sampling random invalid
	// prefixes, exercising transitions within nested objects, strings and numbers.
	rng := rand.New(rand.NewSource(872391))
	seeds := uniqueSeeds()
	for range 12000 {
		data := bytes.Clone(seeds[rng.Intn(len(seeds))])
		for changes := 1 + rng.Intn(4); changes > 0; changes-- {
			pos := rng.Intn(len(data) + 1)
			switch rng.Intn(4) {
			case 0:
				data = append(data[:pos], append([]byte{byte(rng.Intn(256))}, data[pos:]...)...)
			case 1:
				if pos < len(data) {
					data[pos] = byte(rng.Intn(256))
				}
			case 2:
				if pos < len(data) {
					data = append(data[:pos], data[pos+1:]...)
				}
			case 3:
				data = data[:pos]
			}
		}
		checkUniqueOracle(t, data)
	}
}

func TestUniqueAllKeyBytes(t *testing.T) {
	for n := 0; n < 256; n++ {
		key := []byte{byte(n)}
		// The legacy decoder replaces invalid UTF-8 but rejects control bytes,
		// unescaped quotes and backslashes. Compare raw and escaped equivalents.
		var decoded string
		if json.Unmarshal(append(append([]byte{'"'}, key...), '"'), &decoded) != nil {
			continue
		}
		escaped, err := json.Marshal(decoded)
		if err != nil {
			t.Fatal(err)
		}
		data := append([]byte("{\""), key...)
		data = append(data, []byte("\":0,")...)
		data = append(data, escaped...)
		data = append(data, []byte(":1}")...)
		if err := Unique(data); err == nil {
			t.Fatalf("duplicate replacement for byte 0x%02x accepted: %q", n, data)
		}
		checkUniqueOracle(t, data)
	}
}

func FuzzUniqueDecoderParity(f *testing.F) {
	for _, seed := range uniqueSeeds() {
		f.Add(seed)
	}
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		checkUniqueOracle(t, data)
	})
}

func BenchmarkUnique(b *testing.B) {
	data := uniqueSeeds()[0]
	for _, impl := range []struct {
		name string
		fn   func([]byte) error
	}{{"scanner", Unique}, {"decoder-oracle", decoderUnique}} {
		b.Run(impl.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				if err := impl.fn(data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
