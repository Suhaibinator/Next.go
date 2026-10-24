package environment

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func put(t *testing.T, dir, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}
func snapshot(t *testing.T, dir string, mode Mode, base ...string) map[string]string {
	t.Helper()
	pairs, err := Load(dir, mode, base)
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]string{}
	for _, pair := range pairs {
		k, v, _ := strings.Cut(pair, "=")
		result[k] = v
	}
	return result
}
func TestPrecedenceAndExpansion(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, ".env", "A=base\nB=base\nC=base\nD=base\nE=base\nREF=$A/${B}/$UNSET\nFORWARD=$LATER\nLATER=ok\n")
	put(t, dir, ".env.development", "A=mode\nB=mode\nC=mode\nD=mode\n")
	put(t, dir, ".env.local", "A=local\nB=local\nC=local\n")
	put(t, dir, ".env.development.local", "A=mode-local\nB=mode-local\n")
	got := snapshot(t, dir, Development, "A=process", "E=")
	want := map[string]string{"A": "process", "B": "mode-local", "C": "local", "D": "mode", "E": "", "REF": "process/mode-local/", "FORWARD": "ok", "LATER": "ok"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
	if _, ok := os.LookupEnv("FORWARD"); ok {
		t.Fatal("Load mutated the environment")
	}
	if got := snapshot(t, dir, Test); got["A"] != "base" {
		t.Fatalf("test loaded local: %#v", got)
	}
	put(t, dir, ".env.test.local", "A=test-local")
	if snapshot(t, dir, Test)["A"] != "test-local" {
		t.Fatal("test mode local missing")
	}
}
func TestSyntax(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, ".env", "\ufeff# comment\r\nexport A = hello  # trailing\r\nB='  $A\nline  '\nC=\"  $A\\nline\\t\\\"\\$A  \" # comment\nD=\\$A\nE=$(touch nope)\nF=first\nF=last\nG=\"first\nsecond\"\n")
	got := snapshot(t, dir, Production)
	want := map[string]string{"A": "hello", "B": "  $A\nline  ", "C": "  hello\nline\t\"$A  ", "D": "$A", "E": "$(touch nope)", "F": "last", "G": "first\nsecond"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}
func TestErrorsAreBoundedAndRedacted(t *testing.T) {
	for name, input := range map[string]string{"invalid-key": "private-secret bad=value", "unclosed": "SECRET=\"private-secret", "trailing": "SECRET='private-secret' other", "cycle": "SECRET=$OTHER\nOTHER=$SECRET # private-secret", "operator": "SECRET=${private-secret:-x}", "nul": "SECRET=private-secret\x00", "value-limit": "SECRET=" + strings.Repeat("private-secret", 6000), "depth": "A=$B\nB=$C\nC=$D\nD=$E\nE=$F\nF=$G\nG=$H\nH=$I\nI=$J\nJ=$K\nK=$L\nL=$M\nM=$N\nN=$O\nO=$P\nP=$Q\nQ=$R\nR=$S\nS=private-secret"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			put(t, dir, ".env", input)
			_, err := Load(dir, Production, nil)
			if err == nil {
				t.Fatal("accepted invalid file")
			}
			if strings.Contains(err.Error(), "private-secret") {
				t.Fatalf("leaked value: %v", err)
			}
		})
	}
	dir := t.TempDir()
	put(t, dir, ".env", strings.Repeat("x", maxFile+1))
	if _, err := Load(dir, Production, nil); err == nil {
		t.Fatal("oversized file")
	}
	if _, err := Load(dir, "invalid", nil); err == nil {
		t.Fatal("invalid mode")
	}
	if err := os.Remove(filepath.Join(dir, ".env")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".env"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, Production, nil); err == nil {
		t.Fatal("directory accepted")
	}
}
func TestLimitsAndOwnership(t *testing.T) {
	dir := t.TempDir()
	var text strings.Builder
	for i := 0; i < 257; i++ {
		text.WriteString("K" + strings.Repeat("_", i/100) + string(rune('A'+i%26)) + string(rune('A'+i/26)) + "=x\n")
	}
	put(t, dir, ".env", text.String())
	if _, err := Load(dir, Production, nil); err == nil {
		t.Fatal("too many keys")
	}
	put(t, dir, ".env", "VALUE=first")
	first := snapshot(t, dir, Production)
	put(t, dir, ".env", "VALUE=second")
	if first["VALUE"] != "first" {
		t.Fatal("snapshot changed")
	}
	base := []string{"VALUE=a", "VALUE=b"}
	got, err := Load(dir, Production, base)
	if err != nil {
		t.Fatal(err)
	}
	got[0] = "VALUE=c"
	if base[1] != "VALUE=b" {
		t.Fatal("aliased base")
	}
}
func TestPrivateWatchToken(t *testing.T) {
	dir := t.TempDir()
	a, err := Fingerprint(dir, Development)
	if err != nil {
		t.Fatal(err)
	}
	put(t, dir, ".env", "SECRET=one")
	b, _ := Fingerprint(dir, Development)
	put(t, dir, ".env.production", "SECRET=two")
	c, _ := Fingerprint(dir, Development)
	if a == b || b != c {
		t.Fatal("watch scope")
	}
	put(t, dir, ".env", "SECRET=three")
	d, _ := Fingerprint(dir, Development)
	if b == d {
		t.Fatal("missed edit")
	}
}

func TestApplyAtStartupAndFailureAtomicity(t *testing.T) {
	const key = "NEXTGO_ENV_APPLY_TEST_976"
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	put(t, dir, ".env", key+"=loaded")
	if err := Apply(dir, Production); err != nil {
		t.Fatal(err)
	}
	if os.Getenv(key) != "loaded" {
		t.Fatal("not applied")
	}
	put(t, dir, ".env", key+"=overwritten")
	if err := Apply(dir, Production); err != nil {
		t.Fatal(err)
	}
	if os.Getenv(key) != "loaded" {
		t.Fatal("process value lost precedence")
	}
	put(t, dir, ".env", key+"=changed\nBROKEN=\"unterminated")
	if err := Apply(dir, Production); err == nil {
		t.Fatal("accepted invalid file")
	}
	if os.Getenv(key) != "loaded" {
		t.Fatal("partial mutation on parse failure")
	}
}
func TestAggregateExpansionLimit(t *testing.T) {
	dir := t.TempDir()
	var text strings.Builder
	for i := 0; i < 20; i++ {
		text.WriteString("KEY_" + string(rune('A'+i)) + "=$BASE\n")
	}
	put(t, dir, ".env", text.String())
	if _, err := Load(dir, Production, []string{"BASE=" + strings.Repeat("a", maxValue)}); err == nil {
		t.Fatal("aggregate budget not enforced")
	}
}

func TestDepthLimitIncludesUndefinedReferences(t *testing.T) {
	dir := t.TempDir()
	var text strings.Builder
	for i := 0; i < 17; i++ {
		text.WriteString(string(rune('A'+i)) + "=$" + string(rune('B'+i)) + "\n")
	}
	put(t, dir, ".env", text.String())
	for i := 0; i < 20; i++ {
		if _, err := Load(dir, Production, nil); err == nil {
			t.Fatal("undefined reference escaped depth limit")
		}
	}
}
