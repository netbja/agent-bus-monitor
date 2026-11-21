package main

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestConnectEntryOf(t *testing.T) {
	for args, want := range map[string]bus.ACLEntry{
		"service run dispatch": bus.ACLEntryService, "holder": bus.ACLEntryService, "holder --env x": bus.ACLEntryService,
		"holder drain demo": bus.ACLEntryCLI, "service restart dispatch": bus.ACLEntryCLI,
		"backup": bus.ACLEntryAdmin, "restore x.tar": bus.ACLEntryAdmin, "setup": bus.ACLEntryAdmin,
		"report coder x": bus.ACLEntryCLI, "plan load x": bus.ACLEntryCLI, "": bus.ACLEntryCLI,
	} {
		if got := connectEntryOf(strings.Fields(args)); got != want {
			t.Errorf("%q: %v, want %v", args, got, want)
		}
	}
}
