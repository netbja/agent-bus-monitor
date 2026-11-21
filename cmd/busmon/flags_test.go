package main

import (
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
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
		"--delete", "old", "--theme", "light", "--as", "Charlie", "--version", "--new", "--url", "unix:///r.sock"}
	short := []string{"-p", "demo", "-H", "h1", "-n", "5", "-r", "-y", "-l",
		"-d", "old", "-t", "light", "-a", "Charlie", "-V", "-k", "-u", "unix:///r.sock"}
	legacy := []string{"-project", "demo", "-host=h1", "--limit=5", "-reset", "-yes", "-list",
		"-delete", "old", "-theme", "light", "-as", "Charlie", "-version", "-pick", "-url=unix:///r.sock"} // --pick: --new's name before busmon-cli-flags item 5
	want := busmonOpts{project: "demo", host: "h1", limit: 5, reset: true, yes: true, list: true,
		delete: "old", theme: "light", as: "Charlie", version: true, pick: true, url: "unix:///r.sock"}
	for _, args := range [][]string{long, short, legacy} {
		got, set, err := parseFlags(args, io.Discard)
		if err != nil || got != want {
			t.Errorf("%q: %+v, %v\nwant %+v", args, got, err, want)
		}
		for _, name := range []string{"limit", "theme", "as", "new", "url"} {
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

// busmon-cli-flags item 1: -l's MASTER is the registered master agent's
// state, "-" without one; the pilot lease is not it.
func TestProjectTableMasterState(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	out := projectTable([]bus.ProjectSummary{
		{Project: "agent-bus-monitor", Agents: 4, Master: "", MasterState: "working", LastTS: now.UnixMilli()},
		{Project: "quiet", Agents: 1, Master: "pilot", LastTS: now.UnixMilli()},
	}, now)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "MASTER") {
		t.Fatalf("table:\n%s", out)
	}
	if f := strings.Fields(lines[1]); len(f) < 3 || f[2] != "working" {
		t.Fatalf("agent-bus-monitor row %q", lines[1])
	}
	if f := strings.Fields(lines[2]); len(f) < 3 || f[2] != "-" {
		t.Fatalf("no master registered: row %q", lines[2])
	}
}

// busmon-cli-flags item 5: the picker is --new, -k its short form; --pick,
// its old name, still parses and is no longer listed.
func TestNewFlagRenamesPick(t *testing.T) {
	var sb strings.Builder
	if _, _, err := parseFlags([]string{"-h"}, &sb); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	help := sb.String()
	if !strings.Contains(help, "-k, --new") || strings.Contains(help, "pick") {
		t.Fatalf("help:\n%s", help)
	}
	for _, args := range [][]string{{"--new"}, {"-k"}, {"--pick"}, {"-pick"}} {
		o, set, err := parseFlags(args, io.Discard)
		if err != nil || !o.pick || !set["new"] || len(set) != 1 {
			t.Errorf("%q: %+v %v %v", args, o, set, err)
		}
	}
}

// busmon-cli-flags item 4: --url (or REDIS_URL) names the broker by URL, a
// unix socket or an ACL user included; busmon sets REDIS_URL from it, so
// every connection it makes and every busmon it starts uses it. --host
// beside a URL is refused, never silently dropped.
func TestApplyURL(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		env     string
		wantURL string
		err     string
	}{
		{"url flag", []string{"--url", "unix:///tmp/r.sock"}, "", "unix:///tmp/r.sock", ""},
		{"url flag over env", []string{"-u", "redis://busmon:pw@h:6380/0"}, "redis://other:6380", "redis://busmon:pw@h:6380/0", ""},
		{"env alone", nil, "redis://h:6380", "redis://h:6380", ""},
		{"host alone", []string{"-H", "vdr"}, "", "", ""},
		{"host and url", []string{"-H", "vdr", "--url", "redis://h:6380"}, "", "", "--host and --url"},
		{"host and env url", []string{"-H", "vdr"}, "redis://h:6380", "", "REDIS_URL"},
		{"bad url", []string{"--url", "http://h"}, "", "", "--url"},
	}
	for _, c := range cases {
		o, set, err := parseFlags(c.args, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		env := map[string]string{"REDIS_URL": c.env}
		err = applyURL(o, set, func(k string) string { return env[k] }, func(k, v string) error { env[k] = v; return nil })
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: err %v, want %q", c.name, err, c.err)
			}
			continue
		}
		if err != nil || env["REDIS_URL"] != c.wantURL {
			t.Errorf("%s: REDIS_URL %q, err %v; want %q", c.name, env["REDIS_URL"], err, c.wantURL)
		}
	}
}
