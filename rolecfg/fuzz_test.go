package rolecfg

// Fuzz targets (plain `go test` runs only the seeds below). Fuzz one with
//
//	go test ./rolecfg -run '^$' -fuzz='^FuzzShellSplit$' -fuzztime=20s -fuzzminimizetime=0
//
// -fuzzminimizetime=0: the default 60 s minimization of each new interesting
// input stalls the run at 0 execs/s for most of a short -fuzztime.

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzShellSplit: never panics; what it accepts re-splits identically after
// ShellJoin.
func FuzzShellSplit(f *testing.F) {
	for _, s := range []string{"", "a b c", `'a b' "c d" e\ f`, `"x\"y" 'it'"'"'s'`, `unbalanced 'quote`, `"open`, `trailing\`, "tab\tnew\nline", `"$HOME" '$x' \$y`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		words, err := ShellSplit(s)
		if err != nil || !utf8.ValidString(s) {
			return
		}
		again, err := ShellSplit(ShellJoin(words))
		if err != nil {
			t.Fatalf("ShellSplit(ShellJoin(%q)): %v", words, err)
		}
		if len(words) != len(again) || len(words) > 0 && !reflect.DeepEqual(words, again) {
			t.Fatalf("re-split %q -> %q", words, again)
		}
	})
}

// FuzzShellJoinRoundTrip: ShellSplit(ShellJoin(w)) == w for any valid UTF-8
// words (the input is split on NUL into words).
func FuzzShellJoinRoundTrip(f *testing.F) {
	for _, s := range []string{"a", "a\x00b c", "\x00", "it's\x00\"q\"\x00\\\x00$x", "new\nline\x00\ttab", "é\x00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if !utf8.ValidString(s) {
			return
		}
		words := strings.Split(s, "\x00")
		got, err := ShellSplit(ShellJoin(words))
		if err != nil {
			t.Fatalf("ShellSplit(ShellJoin(%q)): %v", words, err)
		}
		if !reflect.DeepEqual(got, words) {
			t.Fatalf("round trip %q -> %q (joined %q)", words, got, ShellJoin(words))
		}
	})
}
