package scripts

import "testing"

func TestModuleDescriptorHasExplicitNativeImportContract(t *testing.T) {
	for _, module := range []Module{{ID: "public-v1", Src: "/vendor/lib-v1.mjs", Asset: true}, {ID: "cdn-v1", Src: "https://cdn.example/library-v1.mjs?locale=fr"}} {
		if checked, err := module.Check(); err != nil || checked != module {
			t.Fatal(checked, err)
		}
	}
	for _, raw := range []string{`null`, `{"id":"a","id":"b","src":"/x.mjs"}`, `{"id":"a","src":"/x.mjs","integrity":"sha384-ignored"}`, `{"id":"a","src":"/x.mjs","crossOrigin":"use-credentials"}`, `{"id":"a","src":"/x.mjs","type":"module"}`, `{"id":"a","src":"/x.mjs","data":{"data-site":"example.test"}}`} {
		if _, err := ParseModule(raw); err == nil {
			t.Fatal("accepted malformed or unsupported import controls", raw)
		}
	}
	for _, module := range []Module{{ID: "", Src: "/x.mjs"}, {ID: "x", Src: "data:text/javascript,export{}"}, {ID: "x", Src: "//cdn.example/x.mjs"}, {ID: "x", Src: "/x.mjs#other"}, {ID: "x", Src: "https://user:secret@cdn.example/x.mjs"}, {ID: "x", Src: "https://cdn.example/x.mjs", Asset: true}} {
		if _, err := module.Check(); err == nil {
			t.Fatal("accepted unsafe module identity", module)
		}
	}
}
