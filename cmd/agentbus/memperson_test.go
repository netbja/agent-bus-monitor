package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/personmem"
)

// Spec 2026-11-21-person-memory-design r2, §10 step 3: agentbus mem person,
// every verb of §4.2; forget and purge are the person's (agentcaller.Refuse),
// purge asks for a typed yes.

// memReader holds entries by "<project>:<id>".
type memReader map[string]personmem.Entry

func (r memReader) Entry(_ context.Context, s personmem.Source) (personmem.Entry, bool, error) {
	e, ok := r[s.Project+":"+s.ID]
	return e, ok, nil
}

const (
	pmTyped = "1795205976983-0" // the person's typed directive
	pmAgent = "1795205970000-0" // master's report
)

// memPersonEnv is a store under a fresh XDG_DATA_HOME and deps around it; an
// empty AGENT_BUS_AGENT and no agent ancestor: the person's own shell.
func memPersonEnv(t *testing.T) (memPersonDeps, map[string]string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	env := map[string]string{"XDG_DATA_HOME": t.TempDir(), "HOME": t.TempDir()}
	rd := memReader{
		"demo:" + pmTyped: {Kind: "cmd", Type: "directive", From: "charlie", Body: "yes, dev first", Human: true, Typed: true},
		"demo:" + pmAgent: {Kind: "report", From: "master", Body: "dev?", Full: "Should PRs go to dev first?"},
		"demo:7-0":        {Kind: "cmd", Type: "directive", From: "charlie", Body: "no sound at night", Human: true, Typed: true},
	}
	d := memPersonDeps{
		Getenv:   func(k string) string { return env[k] },
		Ancestor: func(context.Context) (string, error) { return "", nil },
		Reader:   func(context.Context) (personmem.Reader, func(), error) { return rd, func() {}, nil },
		In:       strings.NewReader(""),
	}
	return d, env
}

// mp runs agentbus mem person with args.
func mp(t *testing.T, d memPersonDeps, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := runMemPerson(context.Background(), args, d, &out, &errw)
	return code, out.String(), errw.String()
}

func TestMemPersonRememberShowListRecall(t *testing.T) {
	d, _ := memPersonEnv(t)
	code, out, errw := mp(t, d, "remember", "merge-policy", "--type", "decision", "--importance", "critical",
		"--from", "demo:"+pmTyped, "--quote", "dev first", "--answers", "demo:"+pmAgent, "--answers-quote", "PRs go to dev",
		"--kind", "practice", "--relates", "applies-to:project-demo")
	if code != 0 || !strings.Contains(out, "20") || !strings.Contains(out, "merge-policy") {
		t.Fatalf("remember: %d %q %q", code, out, errw)
	}
	if code, _, _ := mp(t, d, "remember", "notifications", "--type", "preference", "--importance", "minor", "--from", "demo:7-0", "--quote", "no sound"); code != 0 {
		t.Fatal("second remember")
	}
	code, out, _ = mp(t, d, "show", "merge-policy")
	for _, want := range []string{"topic: merge-policy", "kind: practice", "applies-to: project-demo", `quote: "dev first"`, "question:"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("show lacks %q: %d\n%s", want, code, out)
		}
	}
	code, out, _ = mp(t, d, "list")
	if code != 0 || !strings.Contains(out, "merge-policy") || !strings.Contains(out, "notifications") || !strings.Contains(out, "1") {
		t.Fatalf("list: %d\n%s", code, out)
	}
	code, out, _ = mp(t, d, "recall", "merge")
	if lines := strings.Split(strings.TrimSpace(out), "\n"); code != 0 || len(lines) != 1 || !strings.Contains(lines[0], "dev first") {
		t.Fatalf("recall merge: %d\n%s", code, out)
	}
	code, out, _ = mp(t, d, "recall", "--json", "--type", "preference")
	var hits []map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &hits) != nil || len(hits) != 1 || hits[0]["topic"] != "notifications" || hits[0]["quote"] != "no sound" {
		t.Fatalf("recall --json: %d %s", code, out)
	}
	if code, out, _ = mp(t, d, "recall", "--limit", "1"); code != 0 || strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Fatalf("recall --limit 1: %d\n%s", code, out)
	}
}

// remember's refusals exit 1, write nothing and say why; a bad command line
// exits 2.
func TestMemPersonRememberRefuses(t *testing.T) {
	d, env := memPersonEnv(t)
	for _, c := range []struct {
		args []string
		code int
		why  string
	}{
		{[]string{"remember", "t", "--type", "decision", "--importance", "critical", "--from", "demo:" + pmAgent, "--quote", "dev"}, 1, "typed"},
		{[]string{"remember", "t", "--type", "decision", "--importance", "critical", "--from", "demo:" + pmTyped, "--quote", "main first"}, 1, "quote"},
		{[]string{"remember", "t", "--type", "decision", "--importance", "critical", "--from", "demo:" + pmTyped}, 2, "usage"},
		{[]string{"remember", "t", "--type", "decision", "--importance", "critical", "--quote", "dev"}, 2, "usage"},
		{[]string{"remember", "t", "--type", "decision", "--importance", "critical", "--from", "nope", "--quote", "dev"}, 2, "<project>:<entry>"},
		{[]string{"remember", "t", "--type", "decision", "--importance", "critical", "--from", "demo:" + pmTyped, "--quote", "dev", "--relates", "nocolon"}, 2, "<rel>:<topic>"},
		{[]string{"remember", "t", "--type", "decision", "--importance", "critical", "--from", "demo:" + pmTyped, "--quote", "dev", "--supersedes", "x", "--keep-both"}, 2, "one of"},
	} {
		code, _, errw := mp(t, d, c.args...)
		if code != c.code || !strings.Contains(errw, c.why) {
			t.Errorf("%v: %d %q, want %d %q", c.args, code, errw, c.code, c.why)
		}
	}
	if _, err := os.Stat(env["XDG_DATA_HOME"] + "/agentbus/memory/person/.git"); err == nil {
		t.Fatal("a refused remember made the repository")
	}
}

// forget and purge are the person's: refused under AGENT_BUS_AGENT and
// under an agent's process tree, writing nothing.
func TestMemPersonForgetAndPurgeRefuseAnAgent(t *testing.T) {
	d, env := memPersonEnv(t)
	if code, _, errw := mp(t, d, "remember", "notifications", "--type", "preference", "--importance", "minor", "--from", "demo:7-0", "--quote", "no sound"); code != 0 {
		t.Fatal(errw)
	}
	env["AGENT_BUS_AGENT"] = "master"
	for _, verb := range [][]string{{"forget", "notifications"}, {"purge", "--yes"}} {
		if code, _, errw := mp(t, d, verb...); code != 1 || !strings.Contains(errw, "person's act") {
			t.Errorf("%v as master: %d %q", verb, code, errw)
		}
	}
	delete(env, "AGENT_BUS_AGENT")
	d.Ancestor = func(context.Context) (string, error) { return "coder", nil }
	for _, verb := range [][]string{{"forget", "notifications"}, {"purge", "--yes"}} {
		if code, _, errw := mp(t, d, verb...); code != 1 || !strings.Contains(errw, "agent coder") {
			t.Errorf("%v under coder: %d %q", verb, code, errw)
		}
	}
	d.Ancestor = func(context.Context) (string, error) { return "", nil }
	if code, out, _ := mp(t, d, "list"); code != 0 || !strings.Contains(out, "notifications") {
		t.Fatalf("refused forget wrote: %s", out)
	}
	if code, out, errw := mp(t, d, "forget", "notifications"); code != 0 || !strings.Contains(out, "forgot the topic notifications") {
		t.Fatalf("the person's forget: %d %q %q", code, out, errw)
	}
}

// purge names the commits that go and asks for a typed yes; anything else
// leaves the history; --yes skips the question.
func TestMemPersonPurgeAsks(t *testing.T) {
	d, env := memPersonEnv(t)
	for _, q := range []string{"no sound", "sound"} {
		if code, _, errw := mp(t, d, "remember", "notifications", "--type", "preference", "--importance", "minor", "--from", "demo:7-0", "--quote", q); code != 0 {
			t.Fatal(errw)
		}
	}
	s, err := personmem.Open(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	d.In = strings.NewReader("y\n")
	code, out, _ := mp(t, d, "purge")
	if n, _ := s.Commits(context.Background()); code != 1 || n != 2 || !strings.Contains(out, "2 commits") || !strings.Contains(out, "Type yes") {
		t.Fatalf("purge answered y: %d, %d commits\n%s", code, n, out)
	}
	d.In = strings.NewReader("yes\n")
	if code, out, _ = mp(t, d, "purge"); code != 0 || !strings.Contains(out, "purged") {
		t.Fatalf("purge answered yes: %d\n%s", code, out)
	}
	if n, _ := s.Commits(context.Background()); n != 1 {
		t.Fatalf("%d commits after purge", n)
	}
	d.In = strings.NewReader("")
	if code, out, _ = mp(t, d, "purge", "--yes"); code != 0 || !strings.Contains(out, "purged") {
		t.Fatalf("purge --yes: %d\n%s", code, out)
	}
}

// check names what is wrong and exits 1; a clean store passes.
func TestMemPersonCheck(t *testing.T) {
	d, env := memPersonEnv(t)
	if code, out, _ := mp(t, d, "check"); code != 0 || !strings.Contains(out, "PASS") {
		t.Fatalf("empty check: %d %s", code, out)
	}
	if code, _, errw := mp(t, d, "remember", "notifications", "--type", "preference", "--importance", "minor", "--from", "demo:7-0", "--quote", "no sound"); code != 0 {
		t.Fatal(errw)
	}
	path := env["XDG_DATA_HOME"] + "/agentbus/memory/person/notifications.md"
	b, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), "supersedes", "x", 1)+"\n- id: bad\n  type: fact\n  importance: minor\n  said: x\n  quote: \"q\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := mp(t, d, "check"); code != 1 || !strings.Contains(out, "bad") {
		t.Fatalf("check of a hand edit: %d\n%s", code, out)
	}
}

// Usage: no verb, an unknown verb, show without a topic.
func TestMemPersonUsage(t *testing.T) {
	d, _ := memPersonEnv(t)
	for _, args := range [][]string{nil, {"nope"}, {"show"}, {"forget"}, {"list", "x"}} {
		if code, _, errw := mp(t, d, args...); code != 2 || !strings.Contains(errw, "mem person") {
			t.Errorf("%v: %d %q", args, code, errw)
		}
	}
}

// The production wiring: agentbus mem person reaches runMemPerson through
// runMem, and remember reads the person's typed entry from the broker
// (REDIS_URL, the test broker) into the store under XDG_DATA_HOME.
func TestMemPersonThroughRunMem(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	b, _ := dialMain(t)
	ctx := context.Background()
	id, err := b.TypedCmd(ctx, "charlie", "master", "keep the reports short")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("REDIS_URL", os.Getenv("AGENTBUS_TEST_REDIS_URL"))
	data := t.TempDir()
	env := map[string]string{"XDG_DATA_HOME": data, "HOME": t.TempDir(), "XDG_STATE_HOME": t.TempDir()}
	getenv := func(k string) string { return env[k] }
	var out, errw bytes.Buffer
	code := runMem([]string{"person", "remember", "reports", "--type", "preference", "--importance", "standard",
		"--from", b.Project() + ":" + id, "--quote", "reports short"}, getenv, t.TempDir(), time.Now(), &out, &errw)
	if code != 0 {
		t.Fatalf("remember through runMem: %d %q %q", code, out.String(), errw.String())
	}
	out.Reset()
	if code := runMem([]string{"person", "list"}, getenv, t.TempDir(), time.Now(), &out, &errw); code != 0 || !strings.Contains(out.String(), "reports") {
		t.Fatalf("list: %d %q", code, out.String())
	}
}
