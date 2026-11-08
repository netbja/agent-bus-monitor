package agentcaller

import (
	"context"
	"errors"
	"testing"
)

// Refuse is the one rule both binaries apply before a person's act
// (projects-admin §5.4): AGENT_BUS_AGENT set, or a registered agent among the
// ancestors; an ancestry that cannot be read refuses too, except on a host
// with none to read.
func TestRefuse(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "AGENT_BUS_AGENT" {
				return v
			}
			return ""
		}
	}
	anc := func(caller string, err error) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return caller, err }
	}
	ctx := context.Background()
	for _, c := range []struct {
		name   string
		getenv func(string) string
		anc    func(context.Context) (string, error)
		want   string
	}{
		{"env", env("coder"), anc("", nil), "deleting a project is the person's act, not coder's (run busmon from your own shell)"},
		{"ancestor", env(""), anc("demo/coder", nil), "deleting a project is the person's act, and this runs under the agent demo/coder"},
		{"unreadable", env(""), anc("", errors.New("broker down")), "cannot check that no registered agent runs this (broker down)"},
		{"no ancestry", env(""), anc("", ErrNoAncestry), ""},
		{"person", env(""), anc("", nil), ""},
		{"no ancestor reader", env(""), nil, ""},
	} {
		if got := Refuse(ctx, c.getenv, c.anc, "deleting a project", "busmon"); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
