package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/history"
	"github.com/netbja/agent-bus-monitor/plan"
)

// foureyes on onboarding-b1: the approval checks read the history archive
// (bus.SetApprovalArchive in runPlan), so a yes that rolled out of the cmd
// stream (about a day here) still adopts, and a person's passes still
// counts; a failed archive read is an error, never "not approved".

// planEnv runs plan as agent with HOME set to home ("" none).
func planEnv(t *testing.T, b *bus.Bus, repo, agent, home string, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := runPlan(context.Background(), b, repo, func(k string) string {
		switch k {
		case "AGENT_BUS_AGENT":
			return agent
		case "HOME":
			return home
		}
		return ""
	}, args, &out, &errw)
	return code, out.String(), errw.String()
}

// archiveReplies moves the reply entries to ask from the cmd stream into
// the project's history archive under home, as the archiver and the
// stream's trim would.
func archiveReplies(t *testing.T, b *bus.Bus, r *redis.Client, home, ask string) {
	t.Helper()
	ctx := context.Background()
	key := bus.StreamKey(b.Project(), "cmd")
	msgs, err := r.XRange(ctx, key, "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	dir := historyDir(b.Project(), func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	})
	n := 0
	for _, m := range msgs {
		if m.Values["ref"] != ask || m.Values["type"] != "reply" {
			continue
		}
		f := map[string]string{}
		for k, v := range m.Values {
			f[k], _ = v.(string)
		}
		line, _ := json.Marshal(history.Entry{Stream: "cmd", ID: m.ID, F: f})
		ms, _ := strconv.ParseInt(strings.SplitN(m.ID, "-", 2)[0], 10, 64)
		os.MkdirAll(dir, 0o700)
		day := filepath.Join(dir, time.UnixMilli(ms).UTC().Format("2006-01-02")+".jsonl")
		fh, err := os.OpenFile(day, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		fh.Write(append(line, '\n'))
		fh.Close()
		r.XDel(ctx, key, m.ID)
		n++
	}
	if n == 0 {
		t.Fatalf("no reply to %s in the stream", ask)
	}
}

// brokenArchive makes the project's archive unreadable: its directory is a
// file.
func brokenArchive(t *testing.T, b *bus.Bus, home string) {
	t.Helper()
	dir := historyDir(b.Project(), func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	})
	os.MkdirAll(filepath.Dir(dir), 0o700)
	os.RemoveAll(dir)
	if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPlanDraftYesFromTheArchive(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() {
		r.Del(context.Background(), bus.PlanKey(p), bus.PlanFactsKey(p), bus.PlanIgnoreKey(p), bus.PlanDraftKey(p), bus.AsksKey(p))
	})
	t.Setenv("HERDR_ENV", "")
	b.Status(ctx, "master", "idle", "x", bus.AgentIdent{})
	home := t.TempDir()
	repo := newDraftRepo(t)
	sha := repo.commit(map[string]string{"docs/plans/v0.json": draftPlanV0, "BUILD.md": draftBuildMD})
	ref := "docs/plans/v0.json@" + sha
	d := mustDraft(t, b, repo.dir, "architect", ref)
	if _, _, err := b.Answer(ctx, d.Ask, d.QID, "charlie", "yes", ""); err != nil {
		t.Fatal(err)
	}
	archiveReplies(t, b, r, home, d.Ask)

	// Another home has no archive: the yes is nowhere, nothing approves.
	if code, out, _ := planEnv(t, b, repo.dir, "", t.TempDir(), "draft", "show"); code != 0 || !strings.Contains(out, "not approved") {
		t.Fatalf("show without the archive: %d %s", code, out)
	}
	if code, _, errw := planEnv(t, b, repo.dir, "master", t.TempDir(), "load", ref); code == 0 || !strings.Contains(errw, "awaits the person's yes") {
		t.Fatalf("load without the archive: %d %q", code, errw)
	}
	if code, out, errw := planEnv(t, b, repo.dir, "", home, "draft", "show"); code != 0 || !strings.Contains(out, "approved: the person's yes") {
		t.Fatalf("show with the archive: %d %s %s", code, out, errw)
	}
	if code, out, errw := planEnv(t, b, repo.dir, "master", home, "load", ref); code != 0 || !strings.Contains(out, "the draft the person approved") {
		t.Fatalf("load with the archive: %d %s %s", code, out, errw)
	}
}

func TestPlanDraftArchiveFailureIsAnError(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() {
		r.Del(context.Background(), bus.PlanKey(p), bus.PlanFactsKey(p), bus.PlanIgnoreKey(p), bus.PlanDraftKey(p), bus.AsksKey(p))
	})
	t.Setenv("HERDR_ENV", "")
	b.Status(ctx, "master", "idle", "x", bus.AgentIdent{})
	home := t.TempDir()
	repo := newDraftRepo(t)
	sha := repo.commit(map[string]string{"docs/plans/v0.json": draftPlanV0, "BUILD.md": draftBuildMD})
	d := mustDraft(t, b, repo.dir, "architect", "docs/plans/v0.json@"+sha)
	if _, _, err := b.Answer(ctx, d.Ask, d.QID, "charlie", "yes", ""); err != nil {
		t.Fatal(err)
	}
	archiveReplies(t, b, r, home, d.Ask)
	brokenArchive(t, b, home)
	code, out, errw := planEnv(t, b, repo.dir, "", home, "draft", "show")
	if code == 0 || !strings.Contains(errw, "reading the history archive") || strings.Contains(out, "not approved") {
		t.Fatalf("show over a failed archive read: %d %q %q", code, out, errw)
	}
	if code, _, errw := planEnv(t, b, repo.dir, "master", home, "load", "docs/plans/v0.json@"+sha); code == 0 || !strings.Contains(errw, "reading the history archive") {
		t.Fatalf("load over a failed archive read: %d %q", code, errw)
	}
}

// plan done's person checks read the archive too, and report a failed read
// as one, not as "not answered".
func TestPlanDonePersonFromTheArchive(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	p := b.Project()
	t.Cleanup(func() { r.Del(context.Background(), bus.PlanKey(p), bus.PlanFactsKey(p), bus.AsksKey(p)) })
	t.Setenv("HERDR_ENV", "")
	b.Status(ctx, "master", "idle", "", bus.AgentIdent{})
	home := t.TempDir()
	repo, sha := planRepo(t, personPlan)
	if code, _, errw := planEnv(t, b, repo, "master", home, "load", "docs/plans/v0.22.json@"+sha); code != 0 {
		t.Fatalf("load: %s", errw)
	}
	f, _ := plan.Parse([]byte(personPlan))
	qid := plan.PersonCheckQID(f, f.Tasks[0])
	a, _, err := b.Ask(ctx, bus.AskDef{From: "sentinel", Title: "Release check", Task: "release", Questions: []bus.AskQuestion{
		{ID: qid, Header: "Check", Question: "does it pass?", Options: []bus.AskOption{{Label: "passes"}, {Label: "fails"}}}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Answer(ctx, a, qid, "charlie", "passes", ""); err != nil {
		t.Fatal(err)
	}
	archiveReplies(t, b, r, home, a)
	if code, _, errw := planEnv(t, b, repo, "master", t.TempDir(), "done", "release"); code == 0 || !strings.Contains(errw, "has not answered passes") {
		t.Fatalf("done without the archive: %d %q", code, errw)
	}
	broken := t.TempDir()
	brokenArchive(t, b, broken)
	if code, _, errw := planEnv(t, b, repo, "master", broken, "done", "release"); code == 0 || !strings.Contains(errw, "reading the history archive") {
		t.Fatalf("done over a failed archive read: %d %q", code, errw)
	}
	if code, out, errw := planEnv(t, b, repo, "master", home, "done", "release"); code != 0 || !strings.Contains(out, "recorded done") {
		t.Fatalf("done with the archive: %d %s %s", code, out, errw)
	}
}
