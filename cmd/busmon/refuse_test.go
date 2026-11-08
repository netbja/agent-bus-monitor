package main

import (
	"context"
	"io"
	"testing"
)

// projects-admin §5.4: --delete and --reset refuse an agent caller, however
// the flags are spelled; every other start is the person's or harmless.
func TestRefuseDestructiveForAnAgent(t *testing.T) {
	agent := func(k string) string {
		if k == "AGENT_BUS_AGENT" {
			return "coder"
		}
		return ""
	}
	person := func(string) string { return "" }
	under := func(context.Context) (string, error) { return "demo/coder", nil }
	none := func(context.Context) (string, error) { return "", nil }
	for _, c := range []struct {
		args   []string
		getenv func(string) string
		anc    func(context.Context) (string, error)
		want   string
	}{
		{[]string{"--delete", "demo", "--yes"}, agent, none, "deleting a project is the person's act, not coder's (run busmon from your own shell)"},
		{[]string{"-d=demo"}, person, under, "deleting a project is the person's act, and this runs under the agent demo/coder"},
		{[]string{"-p", "x", "--reset", "-y"}, agent, none, "purging a project's streams is the person's act, not coder's (run busmon from your own shell)"},
		{[]string{"-r", "-p", "x"}, person, under, "purging a project's streams is the person's act, and this runs under the agent demo/coder"},
		{[]string{"--delete", "demo"}, person, none, ""},
		{[]string{"-p", "x"}, agent, under, ""},
		{[]string{"--list"}, agent, under, ""},
	} {
		o, _, err := parseFlags(c.args, io.Discard)
		if err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if got := refuseDestructive(context.Background(), o, c.getenv, c.anc); got != c.want {
			t.Errorf("%v: %q, want %q", c.args, got, c.want)
		}
	}
}
