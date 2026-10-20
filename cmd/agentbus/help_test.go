package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestExpandArgs(t *testing.T) {
	cases := []struct{ in, want []string }{
		{[]string{"-p", "demo", "agents", "-j"}, []string{"--project", "demo", "agents", "--json"}},
		{[]string{"-H", "h1", "-p", "demo", "verdicts", "-P", "12"}, []string{"--host", "h1", "--project", "demo", "verdicts", "--pr", "12"}},
		{[]string{"--project=demo", "cmd", "-r", "T", "-n", "coder", "go"}, []string{"--project", "demo", "cmd", "--ref", "T", "--no-wake", "coder", "go"}},
		{[]string{"subscribe", "-d", "coder"}, []string{"subscribe", "--drain", "coder"}},
		{[]string{"watch", "-s", "0", "coder"}, []string{"watch", "--since", "0", "coder"}},
		// A short another command defines is left alone.
		{[]string{"notify", "-j", "hi"}, []string{"notify", "-j", "hi"}},
		// service logs keeps its -n count.
		{[]string{"-p", "demo", "service", "logs", "-n", "50"}, []string{"--project", "demo", "service", "logs", "-n", "50"}},
		// up passes bootstrap's -y through.
		{[]string{"up", "demo", "-n", "-y"}, []string{"up", "demo", "--dry-run", "-y"}},
		// Internal commands and the scripts' roles verbs: globals only.
		{[]string{"-p", "demo", "service", "run", "dispatch"}, []string{"--project", "demo", "service", "run", "dispatch"}},
		{[]string{"statusline", "-j"}, []string{"statusline", "-j"}},
		{[]string{"roles", "launch", "coder", "demo", "-j"}, []string{"roles", "launch", "coder", "demo", "-j"}},
		{[]string{"-p", "demo", "roles", "doctor", "-j", "-u"}, []string{"--project", "demo", "roles", "doctor", "--json", "--publish"}},
		// An unknown command: only the leading globals.
		{[]string{"-p", "demo", "nope", "-j"}, []string{"--project", "demo", "nope", "-j"}},
		{nil, []string{}},
	}
	for _, c := range cases {
		if got := expandArgs(c.in); !slices.Equal(got, c.want) {
			t.Errorf("expandArgs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHelpRequest(t *testing.T) {
	cases := []struct {
		in   []string
		name string
		ok   bool
	}{
		{[]string{"help"}, "", true},
		{[]string{"--help"}, "", true},
		{[]string{"-h"}, "", true},
		{[]string{"help", "verdict"}, "verdict", true},
		{[]string{"verdict", "--help"}, "verdict", true},
		{[]string{"board", "-h"}, "board", true},
		{[]string{"watch", "--help"}, "watch", true},
		// Only right after the command: a message may say --help.
		{[]string{"report", "coder", "see", "--help"}, "", false},
		{[]string{"notify", "use", "-h"}, "", false},
		{[]string{"agents"}, "", false},
		{nil, "", false},
	}
	for _, c := range cases {
		name, ok := helpRequest(c.in)
		if name != c.name || ok != c.ok {
			t.Errorf("helpRequest(%q) = %q, %v, want %q, %v", c.in, name, ok, c.name, c.ok)
		}
	}
}

// Help runs with no project and no broker: an empty environment and a Redis
// port nothing listens on.
func TestHelpNeedsNoProjectOrBroker(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "agentbus")
	if out, err := runOut("go", "build", "-o", bin, "."); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	run := func(args ...string) (string, string, int) {
		cmd := exec.Command(bin, args...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "REDIS_HOST=127.0.0.1", "REDIS_PORT=1"}
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return stdout.String(), stderr.String(), code
	}
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}, {"-p", "demo", "-h"}} {
		out, errOut, code := run(args...)
		if code != 0 || !strings.HasPrefix(out, "usage: agentbus [flags] <command>") || !strings.Contains(out, "\n  subscribe ") {
			t.Errorf("%q: code %d stdout %q stderr %q", args, code, out, errOut)
		}
	}
	for _, args := range [][]string{{"help", "verdict"}, {"verdict", "--help"}, {"verdict", "-h"}, {"-p", "demo", "verdict", "-h"}} {
		out, errOut, code := run(args...)
		if code != 0 || !strings.HasPrefix(out, "usage: agentbus verdict (--pr <n>") || !strings.Contains(out, "-P, --pr <n>") {
			t.Errorf("%q: code %d stdout %q stderr %q", args, code, out, errOut)
		}
	}
	if out, errOut, code := run("help", "nope"); code != 2 || out != "" || !strings.Contains(errOut, `unknown command "nope"`) {
		t.Errorf("help nope: code %d stdout %q stderr %q", code, out, errOut)
	}
	// No command: the index on stderr, exit 1, and -p was read as the project.
	for _, args := range [][]string{{}, {"-p", "demo"}} {
		out, errOut, code := run(args...)
		if code != 1 || out != "" || !strings.HasPrefix(errOut, "usage: agentbus [flags] <command>") {
			t.Errorf("%q: code %d stdout %q stderr %q", args, code, out, errOut)
		}
	}
}
