package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// retro step 2's CLI (spec §4, §6): propose parses and checks the file
// (rows, evidence, destinations on the base, sensitive fields, owner),
// files it and opens its ask; show prints the outcomes and the base's
// changes; route and withdraw are master's; ask refuses the retro author.

// fakeBase is the delivery base: path -> file; a missing path is absent.
type fakeBase map[string]gitFile

func (f fakeBase) git(file string) (gitFile, error) { return f[file], nil }

func retroPlus(base fakeBase, files map[string]string, sets map[string]int) retroPlusDeps {
	return retroPlusDeps{
		Git:      base.git,
		Archived: func(string) bool { return false },
		Setting:  func(n string) int { return sets[n] },
		Now:      time.Now,
		ReadFile: func(name string) ([]byte, error) {
			if s, ok := files[name]; ok {
				return []byte(s), nil
			}
			return nil, fmt.Errorf("open %s: no such file", name)
		},
	}
}

func retroTable(rows ...string) string {
	return "Root cause: the review read 1-0 late -> trigger -> mechanism -> cause.\n\n" +
		"| n | lesson | evidence | destination | file | change |\n|---|---|---|---|---|---|\n" + strings.Join(rows, "\n") + "\n"
}

func TestRetroProposeCLI(t *testing.T) {
	b, _ := dialMain(t)
	ctx := context.Background()
	roughTask(t, b, "t1") // a reject: coder owes a retro of t1
	board, _ := b.Board(ctx)
	ev := board["t1"].Request.ID // an entry of the cmd stream
	base := fakeBase{"skills/agent-bus/SKILL.md": {Blob: "b1", Lines: 420, Exists: true}, "AGENTS.md": {Blob: "a1", Lines: 50, Exists: true},
		"skills/drift/SKILL.md": {Exists: false}}
	row := func(n int, lesson, dest, file string) string {
		return fmt.Sprintf("| %d | %s | %s | %s | %s | edit the rule in 1 sentence |", n, lesson, ev, dest, file)
	}
	files := map[string]string{
		"ok.md":        retroTable(row(1, "read both verdicts", "skill", "skills/agent-bus/SKILL.md"), row(2, "a drift skill", "new skill", "skills/drift/SKILL.md")),
		"four.md":      retroTable(row(1, "a", "skill", "AGENTS.md"), row(2, "b", "context", "AGENTS.md"), row(3, "c", "context", "AGENTS.md"), row(4, "d", "context", "AGENTS.md")),
		"noev.md":      retroTable("| 1 | a lesson | 9-9 | context | AGENTS.md | edit |"),
		"outside.md":   retroTable(row(1, "a lesson", "context", "README.md")),
		"sensitive.md": retroTable(row(1, "keep api"+"_key=placeholder12 out", "context", "AGENTS.md")),
		"exists.md":    retroTable(row(1, "a new skill", "new skill", "skills/agent-bus/SKILL.md")),
		"absent.md":    retroTable(row(1, "a lesson", "skill", "skills/drift/SKILL.md")),
		"numbered.md":  retroTable(row(2, "a lesson", "context", "AGENTS.md")),
		"sensroot.md":  strings.Replace(retroTable(row(1, "x", "context", "AGENTS.md")), "Root cause: the review", "Root cause: the "+"token=placeholder12 review", 1),
		"noroot.md":    "| n | lesson | evidence | destination | file | change |\n|---|---|---|---|---|---|\n" + row(1, "x", "context", "AGENTS.md") + "\n",
	}
	p := retroPlus(base, files, map[string]int{"retro.max_per_day": 3, "retro.prune_lines": 400})
	run := func(self string, args ...string) (string, error) {
		var out bytes.Buffer
		err := runRetro(ctx, retroDeps{Bus: b, Self: self, Getenv: emptyEnv, Master: "master", Plus: p}, args, &out)
		return out.String(), err
	}
	for _, c := range []struct{ file, want string }{
		{"four.md", "1 to 3 proposals"},
		{"noev.md", "evidence 9-9 is in no stream"},
		{"outside.md", "README.md is not a context destination"},
		{"sensitive.md", "row 1's lesson matches a sensitive pattern"},
		{"exists.md", "exists on the base: a new skill's file must be absent"},
		{"absent.md", "skills/drift/SKILL.md is not on the base"},
		{"numbered.md", "row 1 is numbered 2"},
		{"noroot.md", "no \"Root cause:\" line"},
		{"sensroot.md", "the root cause matches a sensitive pattern"},
		{"missing.md", "no such file"},
	} {
		_, err := run("coder", "propose", "t1", "--file", c.file)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.file, err, c.want)
			continue
		}
		if strings.Contains(err.Error(), "placeholder12") {
			t.Errorf("%s: the refusal shows the value: %v", c.file, err)
		}
	}
	if _, err := run("foureyes", "propose", "t1", "--file", "ok.md"); err == nil || !strings.Contains(err.Error(), "coder's, not foureyes's") {
		t.Fatalf("propose by another agent: %v", err)
	}
	out, err := run("coder", "propose", "t1", "--file", "ok.md")
	if err != nil || !strings.Contains(out, "open for the person") || !strings.Contains(out, "agentbus request done retro-t1") {
		t.Fatalf("propose: %q %v", out, err)
	}
	rec, err := b.Retro(ctx, "t1")
	if err != nil || rec.Rows[0].Blob != "b1" || rec.Rows[1].Blob != bus.RetroBlobAbsent || len(rec.Notes) != 1 || !strings.Contains(rec.Notes[0], "420 lines") {
		t.Fatalf("filed %+v, %v", rec, err)
	}
	// route and withdraw are master's
	if _, err := run("coder", "withdraw", "t1"); err == nil || !strings.Contains(err.Error(), "master's act") {
		t.Fatalf("withdraw by coder: %v", err)
	}
	if _, _, err := b.Answer(ctx, rec.AskID, "p1", "charlie", "yes", ""); err != nil {
		t.Fatal(err)
	}
	if out, err := run("master", "withdraw", "t1"); err != nil || !strings.Contains(out, "now agentbus retro route t1") {
		t.Fatalf("withdraw: %q %v", out, err)
	}
	if out, err := run("master", "route", "t1"); err != nil || !strings.Contains(out, "routed to docs") {
		t.Fatalf("route: %q %v", out, err)
	}
	// show: p1 accepted with its entry, p2 unanswered; the new skill's file
	// appeared on the base since: a conflict; the skill changed: re-read
	base["skills/drift/SKILL.md"] = gitFile{Blob: "d1", Exists: true}
	base["skills/agent-bus/SKILL.md"] = gitFile{Blob: "b2", Exists: true}
	out, err = run("docs", "show", "t1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"retro t1 · routed", "1. accepted (", "2. unanswered", "conflict: skills/drift/SKILL.md exists on the base now", "changed since propose (b2 now)", "note: skills/agent-bus/SKILL.md has 420 lines"} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}
	if out, err := run("docs", "close", "t1", "--pr", "77"); err != nil || !strings.Contains(out, "closed (PR 77)") {
		t.Fatalf("close: %q %v", out, err)
	}
	if out, err := run("", "rejected"); err != nil || !strings.Contains(out, "no rejected proposal") {
		t.Fatalf("rejected: %q %v", out, err)
	}
}

// agentbus ask refuses the retro author, as it does agentbus's own.
func TestAskRefusesTheRetroAuthor(t *testing.T) {
	var out, errw bytes.Buffer
	if code := runAsk(context.Background(), nil, []string{bus.RetroSender}, strings.NewReader(`{}`), emptyEnv, &out, &errw); code == 0 || !strings.Contains(errw.String(), "reserved") {
		t.Fatalf("exit %d, %q", code, errw.String())
	}
}
