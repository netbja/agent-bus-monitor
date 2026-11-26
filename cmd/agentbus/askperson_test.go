package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/agentcaller"
)

// ask-refuses-person (the person, via master): ask <agent> names the ASKING
// agent. "agentbus ask <person>" was accepted, stored as from the person and
// declared them waiting. A person (the recorded identity.as, a human sender
// that is no registered agent, anonymous) or a name that is no known agent
// is refused, with the words to use instead; nothing is stored.
func TestAskRefusesAPersonOrAnUnknownName(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { r.Del(ctx, bus.AsksKey(b.Project())) })
	t.Setenv("AGENTBUS_TERM_ID", "")
	dir := t.TempDir()
	cfg := filepath.Join(dir, "agentbus", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[identity]\nas = \"charlie\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Status(ctx, "coder", "idle", "", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	// An entry named anonymous, as older binaries wrote (#571): still nobody.
	if _, err := b.Status(ctx, "anonymous", "idle", "", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.HumanCmd(ctx, "dana", "coder", "a person who wrote from busmon"); err != nil {
		t.Fatal(err)
	}
	env := func(agent string) func(string) string {
		return func(k string) string {
			switch k {
			case "XDG_CONFIG_HOME", "HOME":
				return dir
			case "AGENT_BUS_AGENT":
				return agent
			}
			return ""
		}
	}
	in := `{"title":"t","questions":[{"id":"q","header":"Q","question":"q?","options":[{"label":"a","recommended":true}]}]}`
	for _, c := range []struct {
		name, self string
		ok         bool
	}{
		{"charlie", "master", false},   // the person's recorded name
		{"charlie", "", false},         // even from the person's own shell: the person answers, never asks
		{"dana", "master", false},      // a human sender, no agent
		{"anonymous", "master", false}, // nobody
		{"ghost", "master", false},     // no known agent
		{"master", "master", true},     // the caller itself
		{"coder", "master", false},     // another agent, registered or not: the asker is the caller
		{"coder", "", false},           // the person's shell, with no agent identity
		{"coder", "coder", true},       // the registered agent itself
		{"dana", "dana", false},        // a caller named like a human sender, no agent entry
	} {
		var out, errw bytes.Buffer
		code := runAsk(ctx, b, []string{c.name}, strings.NewReader(in), env(c.self), &out, &errw)
		if (code == 0) != c.ok {
			t.Errorf("ask %s as %q: exit %d, %q", c.name, c.self, code, errw.String())
		}
		if !c.ok && !strings.Contains(errw.String(), "ask <your agent name>; the person is the one who answers") {
			t.Errorf("ask %s: unclear refusal %q", c.name, errw.String())
		}
	}
	// The person's shell says why it cannot ask.
	{
		var out, errw bytes.Buffer
		if code := runAsk(ctx, b, []string{"coder"}, strings.NewReader(in), env(""), &out, &errw); code == 0 || !strings.Contains(errw.String(), "a person never asks") {
			t.Errorf("ask coder from the person's shell: exit %d, %q", code, errw.String())
		}
	}
	// The person's stray entry (today's bug: an earlier ask <person> made
	// one) does not make them an agent; an agent of the person's name asks.
	if _, err := b.Status(ctx, "charlie", "waiting", "1 question(s): x", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := runAsk(ctx, b, []string{"charlie"}, strings.NewReader(in), env("master"), &out, &errw); code == 0 || !strings.Contains(errw.String(), "is the person") {
		t.Errorf("ask charlie with a stray entry: exit %d, %q", code, errw.String())
	}
	out.Reset()
	errw.Reset()
	if code := runAsk(ctx, b, []string{"charlie"}, strings.NewReader(in), env("charlie"), &out, &errw); code != 0 {
		t.Errorf("an agent of the person's name: exit %d, %q", code, errw.String())
	}
	asks, _ := b.Asks(ctx)
	for _, a := range asks {
		if a.From != "master" && a.From != "coder" && a.From != "charlie" {
			t.Errorf("an ask stored from %q", a.From)
		}
	}
	if snaps, _ := b.Agents(ctx); snaps["ghost"].State != "" || snaps["dana"].State != "" {
		t.Errorf("a refused ask declared someone waiting: %+v", snaps)
	}
}

// ask-refuses-person: an answer is the person's. agentbus answer from an
// agent (AGENT_BUS_AGENT, or inside an agent's terminal) is refused, with or
// without --as: no agent answers in the person's name.
func TestAnswerRefusedFromAnAgent(t *testing.T) {
	for _, env := range []map[string]string{
		{"AGENT_BUS_AGENT": "master"},
		{"AGENTBUS_TERM_ID": "t4"},
	} {
		for _, args := range [][]string{
			{"1-0", "q", "--choice", "a"},
			{"1-0", "q", "--choice", "a", "--as", "charlie"},
		} {
			var out, errw bytes.Buffer
			getenv := func(k string) string { return env[k] }
			if code := runAnswer(context.Background(), nil, args, getenv, nil, &out, &errw); code == 0 || !strings.Contains(errw.String(), "the person's") {
				t.Errorf("%v %v: exit %d, %q", env, args, code, errw.String())
			}
		}
	}
}

// retro §5 (step 2): the answer's own guard holds with both variables unset:
// agentbus answer refuses through agentcaller.Refuse, so an agent that
// clears AGENT_BUS_AGENT and AGENTBUS_TERM_ID is still refused from its
// process tree, and an unreadable ancestry refuses too. The person's shell
// (no agent among the ancestors, or a host with no ancestry) answers.
func TestAnswerRefusedFromAnAgentsProcessTree(t *testing.T) {
	none := func(string) string { return "" }
	for _, c := range []struct {
		name     string
		ancestor func(context.Context) (string, error)
		want     string
	}{
		{"under an agent", func(context.Context) (string, error) { return "demo/coder", nil }, "runs under the agent demo/coder"},
		{"unreadable ancestry", func(context.Context) (string, error) { return "", errors.New("ps failed") }, "cannot check that no registered agent runs this"},
	} {
		var out, errw bytes.Buffer // a nil bus: the refusal comes before any read
		if code := runAnswer(context.Background(), nil, []string{"1-0", "q", "--choice", "a"}, none, c.ancestor, &out, &errw); code == 0 || !strings.Contains(errw.String(), c.want) {
			t.Errorf("%s: exit %d, %q", c.name, code, errw.String())
		}
	}
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { r.Del(ctx, bus.AsksKey(b.Project())) })
	t.Setenv("AGENTBUS_TERM_ID", "")
	in := `{"title":"Two","questions":[
	  {"id":"q1","header":"One","question":"First?","options":[{"label":"yes"},{"label":"no"}]},
	  {"id":"q2","header":"Two","question":"Second?","options":[{"label":"yes"},{"label":"no"}]}]}`
	var out, errw bytes.Buffer
	if code := runAsk(ctx, b, []string{"master"}, strings.NewReader(in), asMaster, &out, &errw); code != 0 {
		t.Fatalf("ask: %d %s", code, errw.String())
	}
	id := strings.Fields(out.String())[0]
	for qid, ancestor := range map[string]func(context.Context) (string, error){
		"q1": func(context.Context) (string, error) { return "", nil },                       // the person's shell
		"q2": func(context.Context) (string, error) { return "", agentcaller.ErrNoAncestry }, // a host with no ancestry
	} {
		out.Reset()
		errw.Reset()
		if code := runAnswer(ctx, b, []string{id, qid, "--choice", "yes"}, none, ancestor, &out, &errw); code != 0 {
			t.Errorf("%s: exit %d, %q", qid, code, errw.String())
		}
	}
}
