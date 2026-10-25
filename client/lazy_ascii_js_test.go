//go:build js && wasm

package client

import (
	"regexp"
	"strings"
	"testing"
)

func TestLazyDescriptorMatchesLegacyHexFilenameAndEpoch(t *testing.T) {
	oldRegistry, oldModules := registry, lazyModules
	t.Cleanup(func() { registry, lazyModules = oldRegistry, oldModules })
	registry = map[string]Mount{}
	epoch := strings.Repeat("b", 64)
	digest := strings.Repeat("a", 64)
	filePattern := regexp.MustCompile(`^module-[a-f0-9]{64}\.wasm$`)
	epochPattern := regexp.MustCompile(`^[a-f0-9]{64}$`)
	base := LazyModule{File: "module-" + digest + ".wasm", Digest: digest, Epoch: epoch, Size: 1}
	check := func(module LazyModule) {
		t.Helper()
		lazyModules = map[string]*lazyState{}
		accepted := true
		func() {
			defer func() {
				if recover() != nil {
					accepted = false
				}
			}()
			RegisterLazy("test", module)
		}()
		want := filePattern.MatchString(module.File) && module.File == "module-"+module.Digest+".wasm" && epochPattern.MatchString(module.Epoch)
		if accepted != want {
			t.Errorf("descriptor%+v accepted%v want%v", module, accepted, want)
		}
	}
	check(base)
	for _, value := range []string{"", strings.Repeat("a", 63), strings.Repeat("a", 65), digest + "\n", "K" + digest[1:], string([]byte{0xff}) + digest[1:]} {
		m := base
		m.Digest = value
		m.File = "module-" + value + ".wasm"
		check(m)
		m = base
		m.Epoch = value
		check(m)
	}
	for _, file := range []string{"Module-" + digest + ".wasm", "module-" + digest + "xwasm", base.File + "\n", base.File + "/tail", "../" + base.File, "module-" + strings.Repeat("c", 64) + ".wasm"} {
		m := base
		m.File = file
		check(m)
	}
	for c := 0; c < 256; c++ {
		value := digest[:31] + string([]byte{byte(c)}) + digest[32:]
		m := base
		m.Digest = value
		m.File = "module-" + value + ".wasm"
		check(m)
		m = base
		m.Epoch = value
		check(m)
		m = base
		m.File = base.File[:7] + string([]byte{byte(c)}) + base.File[8:]
		check(m)
	}
}
