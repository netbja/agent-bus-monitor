package main

import (
	"errors"
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/cli"
)

func TestBusmonFlagTableIsValid(t *testing.T) {
	if err := cli.ValidateFlags(busmonFlags); err != nil {
		t.Fatal(err)
	}
	for _, f := range busmonFlags {
		if f.Short == 0 {
			t.Errorf("--%s has no short form", f.Long)
		}
	}
}

func TestShortFlagsEqualLongFlags(t *testing.T) {
	long := []string{"--project", "demo", "--host", "h1", "--limit", "5", "--reset", "--yes", "--list",
		"--delete", "old", "--theme", "light", "--as", "Charlie", "--version", "--pick"}
	short := []string{"-p", "demo", "-H", "h1", "-n", "5", "-r", "-y", "-l",
		"-d", "old", "-t", "light", "-a", "Charlie", "-V", "-k"}
	legacy := []string{"-project", "demo", "-host=h1", "--limit=5", "-reset", "-yes", "-list",
		"-delete", "old", "-theme", "light", "-as", "Charlie", "-version", "-pick"}
	want := busmonOpts{project: "demo", host: "h1", limit: 5, reset: true, yes: true, list: true,
		delete: "old", theme: "light", as: "Charlie", version: true, pick: true}
	for _, args := range [][]string{long, short, legacy} {
		got, set, err := parseFlags(args, io.Discard)
		if err != nil || got != want {
			t.Errorf("%q: %+v, %v\nwant %+v", args, got, err, want)
		}
		for _, name := range []string{"limit", "theme", "as"} {
			if !set[name] {
				t.Errorf("%q: %s not reported as set", args, name)
			}
		}
	}
}

func TestFlagDefaults(t *testing.T) {
	got, set, err := parseFlags(nil, io.Discard)
	if err != nil || got != (busmonOpts{limit: 25}) || len(set) != 0 {
		t.Fatalf("%+v %v %v", got, set, err)
	}
}

func TestBusmonHelpListsEachFlagOnce(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}} {
		var sb strings.Builder
		_, _, err := parseFlags(args, &sb)
		if !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("%q: err %v", args, err)
		}
		out := sb.String()
		if !strings.HasPrefix(out, "usage: busmon [flags]") ||
			strings.Count(out, "--project") != 1 || !strings.Contains(out, "-p, --project <project>") ||
			!strings.Contains(out, "-n, --limit <n>") || !strings.Contains(out, "examples:") {
			t.Errorf("%q:\n%s", args, out)
		}
	}
}

func TestUnknownFlagFails(t *testing.T) {
	var sb strings.Builder
	if _, _, err := parseFlags([]string{"-z"}, &sb); err == nil || errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err %v", err)
	}
	if !strings.Contains(sb.String(), "-z") {
		t.Fatalf("no message: %q", sb.String())
	}
}
