package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

func ideaEnv(kv ...string) func(string) string {
	m := map[string]string{"XDG_CONFIG_HOME": "/nonexistent-ideas-test"}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

func runIdeaT(t *testing.T, b *bus.Bus, env func(string) string, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := runIdea(context.Background(), b, args, env, &out, &errw)
	return code, out.String(), errw.String()
}

// add is open to agents (by is the agent) and to a person (--as); it
// always starts at "to discuss", whatever is typed.
func TestIdeaAdd(t *testing.T) {
	b := notifyBus(t)
	agent := ideaEnv("AGENT_BUS_AGENT", "coder", "HERDR_PANE_ID", "w1:p2")
	code, out, errw := runIdeaT(t, b, agent, "add", "busmon", "update", "without", "setup.sh", "--text", "why not", "--source", "memory")
	if code != 0 || !strings.Contains(out, "i1") {
		t.Fatalf("add %d %q %q", code, out, errw)
	}
	if code, _, errw := runIdeaT(t, b, agent, "add", "t", "--as", "charlie"); code == 0 || !strings.Contains(errw, "agent") {
		t.Fatalf("an agent added as a person: %d %q", code, errw)
	}
	if code, _, errw := runIdeaT(t, b, ideaEnv(), "add", "t"); code == 0 || !strings.Contains(errw, "--as") {
		t.Fatalf("no author: %d %q", code, errw)
	}
	if code, _, errw := runIdeaT(t, b, ideaEnv(), "add", "a person's idea", "--as", "charlie"); code != 0 {
		t.Fatalf("a person's add: %d %q", code, errw)
	}
	if code, _, _ := runIdeaT(t, b, ideaEnv(config.EnvName("identity.as"), "charlie"), "add", "from identity.as"); code != 0 {
		t.Fatal("identity.as is the author")
	}
	ideas, _ := b.Ideas(context.Background())
	if len(ideas) != 3 || ideas[0].By != "coder" || ideas[0].Title != "busmon update without setup.sh" || ideas[0].Text != "why not" ||
		ideas[0].Source != "memory" || ideas[1].By != "charlie" || ideas[2].By != "charlie" {
		t.Fatalf("ideas %+v", ideas)
	}
	for _, it := range ideas {
		if it.State != bus.IdeaToDiscuss {
			t.Fatalf("%s starts at %q", it.ID, it.State)
		}
	}
}

// set is a person command: refused in an agent's environment or a herdr
// pane, needs a name (--as or identity.as, never pilot), and passes the
// state it read as the compare-and-set's expected one.
func TestIdeaSet(t *testing.T) {
	b := notifyBus(t)
	ctx := context.Background()
	id, _ := b.AddIdea(ctx, bus.IdeaDef{Title: "t", By: "coder"})
	for _, env := range []func(string) string{
		ideaEnv("AGENT_BUS_AGENT", "coder"),
		ideaEnv("HERDR_PANE_ID", "w1:p2"),
		ideaEnv("AGENT_BUS_AGENT", "anonymous"),
	} {
		if code, _, errw := runIdeaT(t, b, env, "set", id, "decided", "--as", "charlie"); code == 0 || !strings.Contains(errw, "person") {
			t.Fatalf("set from an agent's environment: %d %q", code, errw)
		}
	}
	if code, _, errw := runIdeaT(t, b, ideaEnv(), "set", id, "decided"); code == 0 || !strings.Contains(errw, "--as") {
		t.Fatalf("set without a name: %d %q", code, errw)
	}
	if code, _, _ := runIdeaT(t, b, ideaEnv(), "set", id, "finished", "--as", "charlie"); code != 2 {
		t.Fatal("an unknown state was not a usage error")
	}
	code, out, errw := runIdeaT(t, b, ideaEnv(), "set", id, "decided", "--as", "charlie")
	if code != 0 || !strings.Contains(out, "to discuss → decided") {
		t.Fatalf("set %d %q %q", code, out, errw)
	}
	// --from: the state the person saw; a stale one is refused.
	if code, _, errw := runIdeaT(t, b, ideaEnv(), "set", id, "doing", "--from", "discuss", "--as", "charlie"); code != 1 || !strings.Contains(errw, "decided") {
		t.Fatalf("a stale --from: %d %q", code, errw)
	}
	if code, _, errw := runIdeaT(t, b, ideaEnv(config.EnvName("identity.as"), "dana"), "set", id, "doing", "--from", "decided"); code != 0 {
		t.Fatalf("identity.as names the setter: %d %q", code, errw)
	}
	ideas, _ := b.Ideas(ctx)
	if ideas[0].State != bus.IdeaDoing || len(ideas[0].Log) != 2 || ideas[0].Log[1].By != "dana" {
		t.Fatalf("idea %+v", ideas[0])
	}
}

func TestIdeaList(t *testing.T) {
	b := notifyBus(t)
	ctx := context.Background()
	a, _ := b.AddIdea(ctx, bus.IdeaDef{Title: "first", By: "coder"})
	b.AddIdea(ctx, bus.IdeaDef{Title: "second", By: "master"})
	if err := b.SetIdeaState(ctx, a, bus.IdeaToDiscuss, bus.IdeaDropped, "charlie"); err != nil {
		t.Fatal(err)
	}
	_, out, _ := runIdeaT(t, b, ideaEnv(), "list")
	if !strings.Contains(out, "i1") || !strings.Contains(out, "dropped") || !strings.Contains(out, "second") || !strings.Contains(out, "charlie") {
		t.Fatalf("list %q", out)
	}
	_, out, _ = runIdeaT(t, b, ideaEnv(), "list", "--state", "discuss")
	if strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Fatalf("filtered %q", out)
	}
	_, out, _ = runIdeaT(t, b, ideaEnv(), "list", "--json")
	var got []bus.Idea
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 2 || got[0].Log[0].By != "charlie" {
		t.Fatalf("json %q %v", out, err)
	}
	_, out, _ = runIdeaT(t, b, ideaEnv(), "list", "--state", "doing")
	if !strings.Contains(out, "no idea") {
		t.Fatalf("empty %q", out)
	}
}

func TestIdeaGuard(t *testing.T) {
	pane := ideaEnv("HERDR_PANE_ID", "w1:p2")
	if identityGuard(pane, "idea", []string{"list"}) != nil {
		t.Fatal("list only reads")
	}
	if identityGuard(pane, "idea", []string{"set", "i1", "decided"}) != nil {
		t.Fatal("set refuses a pane itself, with its own words")
	}
	if identityGuard(pane, "idea", []string{"add", "t"}) == nil {
		t.Fatal("add writes as the sender")
	}
}

// reviewer-codex (the add's ambiguous outcome): idea add always sends a
// request id; --request reuses one, so a retry after a lost reply finds the
// idea it made. A malformed id is a usage error.
func TestIdeaAddRequest(t *testing.T) {
	b := notifyBus(t)
	env := ideaEnv("AGENT_BUS_AGENT", "coder")
	code, out, errw := runIdeaT(t, b, env, "add", "once", "--request", "req-0001-abcd")
	if code != 0 || !strings.Contains(out, "i1") {
		t.Fatalf("add %d %q %q", code, out, errw)
	}
	code, out, _ = runIdeaT(t, b, env, "add", "once", "--request", "req-0001-abcd")
	if code != 0 || !strings.Contains(out, "i1") {
		t.Fatalf("the retry %d %q", code, out)
	}
	if ideas, _ := b.Ideas(context.Background()); len(ideas) != 1 {
		t.Fatalf("%d ideas after a retry", len(ideas))
	}
	if code, _, _ := runIdeaT(t, b, env, "add", "x", "--request", "a|b"); code != 2 {
		t.Fatal("a malformed request id is not a usage error")
	}
	if code, _, _ := runIdeaT(t, b, env, "add", "twice"); code != 0 {
		t.Fatal("an add without --request")
	}
	if ideas, _ := b.Ideas(context.Background()); len(ideas) != 2 {
		t.Fatalf("%d ideas", len(ideas))
	}
}
