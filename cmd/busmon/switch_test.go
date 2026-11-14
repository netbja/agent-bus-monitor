package main

import (
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The switch's argv is rebuilt from the parsed options, not edited as text
// (project-switch note §2.2): one --project=<target> whatever spelling was
// typed; host, as, theme and limit come back as --<long>=<value>; the
// one-shot actions are never carried; re-parsing gives the same options but
// the project.
func TestSwitchArgv(t *testing.T) {
	for _, spelling := range [][]string{{"-p", "a"}, {"-p=a"}, {"--project", "a"}, {"--project=a"}, {"-project", "a"}} {
		args := append(append([]string{}, spelling...), "-H", "vdr", "--as=charlie", "-t", "dark", "--limit", "50")
		o, set, err := parseFlags(args, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		got := switchArgv(o, set, "b")
		if n := slices.IndexFunc(got, func(a string) bool { return strings.Contains(a, "project") }); n < 0 || got[n] != "--project=b" ||
			slices.ContainsFunc(got[n+1:], func(a string) bool { return strings.Contains(a, "project") }) {
			t.Fatalf("%v: %v", spelling, got)
		}
		for _, want := range []string{"--host=vdr", "--as=charlie", "--theme=dark", "--limit=50"} {
			if !slices.Contains(got, want) {
				t.Errorf("%v: %v lacks %s", spelling, got, want)
			}
		}
		again, _, err := parseFlags(got, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		want := o
		want.project = "b"
		if !reflect.DeepEqual(again, want) {
			t.Errorf("%v: re-parsed %+v, want %+v", spelling, again, want)
		}
	}
	o, set, err := parseFlags([]string{"-p", "a", "--reset", "-y", "--list", "--pick", "-V", "--delete", "x"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if got := switchArgv(o, set, "b"); !slices.Equal(got, []string{"--project=b"}) {
		t.Fatalf("one-shot flags carried: %v", got)
	}
}
