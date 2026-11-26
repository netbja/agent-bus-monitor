package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/update"
)

// TestUpdateArgs: the forms of agentbus update, and the bad ones.
func TestUpdateArgs(t *testing.T) {
	for _, c := range []struct {
		args []string
		want updateArgs
		bad  bool
	}{
		{nil, updateArgs{}, false},
		{[]string{"--check"}, updateArgs{check: true}, false},
		{[]string{"--yes"}, updateArgs{yes: true}, false},
		{[]string{"--list"}, updateArgs{list: true}, false},
		{[]string{"--rollback"}, updateArgs{rollback: true}, false},
		{[]string{"--rollback", "20261120-1415-v0.31.0-v0.32.0", "--yes"}, updateArgs{rollback: true, id: "20261120-1415-v0.31.0-v0.32.0", yes: true}, false},
		{[]string{"--restore", "x1"}, updateArgs{restore: true, id: "x1"}, false},
		{[]string{"--restore"}, updateArgs{}, true},
		{[]string{"--check", "--rollback"}, updateArgs{}, true},
		{[]string{"--list", "--yes"}, updateArgs{}, true},
		{[]string{"extra"}, updateArgs{}, true},
		{[]string{"--rollback", "../x"}, updateArgs{}, true},
		{[]string{"--auto"}, updateArgs{auto: true}, false},
		{[]string{"--auto", "--check"}, updateArgs{auto: true, check: true}, false},
		{[]string{"--auto", "--yes"}, updateArgs{}, true}, // the setting is its yes (quiet-update-auto §4.3)
		{[]string{"--auto", "--list"}, updateArgs{}, true},
		{[]string{"--auto", "--rollback"}, updateArgs{}, true},
	} {
		got, err := parseUpdateArgs(c.args)
		if (err != nil) != c.bad || (!c.bad && got != c.want) {
			t.Errorf("%v: %+v %v", c.args, got, err)
		}
	}
}

// TestUpdateRefusesAnAgent: updating agentbus is the person's act (§5):
// AGENT_BUS_AGENT or a registered agent among the ancestors refuses update,
// --rollback and --restore; --check and --list are read-only and pass.
func TestUpdateRefusesAnAgent(t *testing.T) {
	ctx := context.Background()
	agentEnv := func(k string) string {
		if k == "AGENT_BUS_AGENT" {
			return "coder"
		}
		return ""
	}
	none := func(string) string { return "" }
	noAncestor := func(context.Context) (string, error) { return "", nil }
	ancestor := func(context.Context) (string, error) { return "coder", nil }
	unreadable := func(context.Context) (string, error) { return "", errors.New("no /proc") }
	for _, a := range []updateArgs{{}, {yes: true}, {rollback: true}, {restore: true, id: "x"}, {auto: true}} {
		if why := updateRefusal(ctx, a, agentEnv, noAncestor); !strings.Contains(why, "updating agentbus") {
			t.Errorf("%+v under AGENT_BUS_AGENT: %q", a, why)
		}
		if why := updateRefusal(ctx, a, none, ancestor); why == "" {
			t.Errorf("%+v under an agent ancestor: passed", a)
		}
		if why := updateRefusal(ctx, a, none, unreadable); why == "" {
			t.Errorf("%+v with an unreadable ancestry: passed", a)
		}
		if why := updateRefusal(ctx, a, none, noAncestor); why != "" {
			t.Errorf("%+v by the person: %q", a, why)
		}
	}
	for _, a := range []updateArgs{{check: true}, {list: true}, {auto: true, check: true}} {
		if why := updateRefusal(ctx, a, agentEnv, ancestor); why != "" {
			t.Errorf("%+v is read-only: %q", a, why)
		}
	}
}

// update --auto installs (when updates.auto = install, InstallPass's own
// test); --auto --check only reads (quiet-update-auto §4.3).
func TestAutoPassChoice(t *testing.T) {
	if reflect.ValueOf(autoPass(true)).Pointer() != reflect.ValueOf(update.CheckPass).Pointer() ||
		reflect.ValueOf(autoPass(false)).Pointer() != reflect.ValueOf(update.InstallPass).Pointer() {
		t.Fatal("--auto and --auto --check choose the wrong pass")
	}
}
