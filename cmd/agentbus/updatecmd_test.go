package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
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

// quiet-update-auto §4.6: a notice is a report from updater naming the
// project's escalation target (its env file's AGENT_BUS_ESCALATE_TO, master
// without one), then its notice to that target, wake=no unless it wakes.
func TestUpdateNotify(t *testing.T) {
	b, _ := dialMain(t)
	ctx := context.Background()
	home := t.TempDir()
	getenv := func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	busFor := func(p string) (*bus.Bus, error) {
		if p != b.Project() {
			return nil, errors.New("broker down")
		}
		return b, nil
	}
	n := update.Notice{Kind: update.NoticeInstalled, Key: "k1", Text: "agentbus v1 -> v2 installed automatically", Projects: []string{b.Project()}}
	if err := updateNotify(ctx, busFor, getenv, n); err != nil {
		t.Fatal(err)
	}
	paths, _ := service.PathsFrom(getenv)
	if err := os.MkdirAll(filepath.Dir(paths.EnvFile(b.Project())), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.EnvFile(b.Project()), []byte("AGENT_BUS_ESCALATE_TO=\"lead\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	n.Kind, n.Wake, n.Text = update.NoticeFailed, true, "agentbus update --auto: v1 -> v2 failed"
	n.Projects = []string{b.Project(), "elsewhere"}
	if err := updateNotify(ctx, busFor, getenv, n); err == nil || !strings.Contains(err.Error(), "project elsewhere: broker down") {
		t.Fatalf("a project's broker down: %v", err)
	}
	reps, err := b.Reports(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	cmds, _, err := b.Recent(ctx, []string{"cmd"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 2 || len(cmds) != 2 {
		t.Fatalf("reports %+v\ncmds %+v", reps, cmds)
	}
	for i, want := range []struct {
		to   string
		wake bool
		text string
	}{{"master", false, "installed automatically"}, {"lead", true, "failed"}} {
		r, c := reps[i], cmds[i]
		if r.Agent != "updater" || r.To != want.to || !strings.Contains(r.Message, want.text) {
			t.Errorf("report %d %+v", i, r)
		}
		if c.From != "updater" || c.Target != want.to || c.Ref != r.ID || c.NoWake == want.wake || !strings.HasPrefix(c.Message, "Report "+r.ID+" for you") {
			t.Errorf("notice %d %+v", i, c)
		}
	}
}
