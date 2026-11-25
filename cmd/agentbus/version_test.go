package main

import (
	"bytes"
	"slices"
	"testing"

	"github.com/netbja/agent-bus-monitor/buildinfo"
	"github.com/netbja/agent-bus-monitor/bus"
)

// --version and -v are the command version, as the first word only;
// version itself prints protocol and build (main checks it before the
// project-required exit).
func TestVersionAlias(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"-v"}, {"version"}} {
		if got := versionAlias(args); !slices.Equal(got, []string{"version"}) {
			t.Errorf("%q -> %q", args, got)
		}
	}
	for _, args := range [][]string{nil, {"-vv"}, {"--verbose"}, {"status", "-v"}, {"versions"}, {"request", "--version"}} {
		if got := versionAlias(args); !slices.Equal(got, args) {
			t.Errorf("%q -> %q", args, got)
		}
	}
	var out bytes.Buffer
	printVersion(&out)
	want := "agentbus protocol v" + itoa(bus.ProtocolVersion) + "\nagentbus version " + buildinfo.Version + "\n"
	if out.String() != want {
		t.Errorf("printed %q, want %q", out.String(), want)
	}
}
