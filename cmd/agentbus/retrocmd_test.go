package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func emptyEnv(string) string { return "" }

func ancestorIs(name string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return name, nil }
}

// roughTask: a task of coder's, accepted, rejected once, then done through
// request done; returns what done printed.
func roughTask(t *testing.T, b *bus.Bus, task string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := b.Request(ctx, task, "master", "coder", "", "build it", 0); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, task, "coder"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	if _, err := b.AppendVerdict(ctx, bus.Verdict{Subject: task, Author: "coder", Reviewer: "foureyes", Decision: "reject", Message: "the format check is a substring match"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	var out bytes.Buffer
	if err := runRequest(ctx, b, "coder", []string{"done", task, "merged"}, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// request done prints the retro line when the task had friction, and
// nothing more on a green task.
func TestRequestDoneSaysRetroDue(t *testing.T) {
	b := dialDispatchMain(t)
	ctx := context.Background()
	if out := roughTask(t, b, "rough"); !strings.HasSuffix(out, "\nretro due: retro-rough (1 reject)\n") {
		t.Errorf("done printed %q", out)
	}
	if _, err := b.Request(ctx, "green", "master", "coder", "", "x", 0); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, "green", "coder"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runRequest(ctx, b, "coder", []string{"done", "green", "ok"}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "retro") || strings.Count(out.String(), "\n") != 1 {
		t.Errorf("green done printed %q", out.String())
	}
}

// retro request is the person's: refused with AGENT_BUS_AGENT, and refused
// under an agent's process tree even with the env unset.
func TestRetroRequestRefusesAgents(t *testing.T) {
	b := dialDispatchMain(t)
	roughTask(t, b, "rough")
	env := func(k string) string {
		if k == "AGENT_BUS_AGENT" {
			return "coder"
		}
		return ""
	}
	cases := []retroDeps{
		{Bus: b, Getenv: env, Ancestor: ancestorIs(""), Master: "master"},
		{Bus: b, Getenv: emptyEnv, Ancestor: ancestorIs("agent-bus-monitor/coder"), Master: "master"},
	}
	for i, d := range cases {
		err := runRetro(context.Background(), d, []string{"request", "rough"}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "person's act") {
			t.Errorf("case %d: %v", i, err)
		}
	}
}

// The person's retro goes to the owner while it is registered, with the
// person's signal; for an ended pop it goes to master.
func TestRetroRequestRoutes(t *testing.T) {
	b := dialDispatchMain(t)
	ctx := context.Background()
	roughTask(t, b, "rough") // retro-rough already sent by done
	d := retroDeps{Bus: b, Getenv: emptyEnv, Ancestor: ancestorIs(""), Master: "master"}
	var out bytes.Buffer
	if err := runRetro(ctx, d, []string{"request", "rough"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "retro retro-rough already open for coder (1 reject, 1 person)\n" {
		t.Errorf("printed %q", got)
	}

	if _, err := b.Status(ctx, "coder", "idle", "", bus.AgentIdent{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Request(ctx, "fixed", "master", "coder", "", "x", 0); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptRequest(ctx, "fixed", "coder"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CompleteRequest(ctx, "fixed", "coder", "ok"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runRetro(ctx, d, []string{"request", "fixed"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "retro retro-fixed sent to coder (1 person)\n" {
		t.Errorf("printed %q", got)
	}
	board, _ := b.Board(ctx)
	if e := board["retro-fixed"]; e.Request == nil || e.Request.Target != "coder" {
		t.Errorf("retro-fixed %+v", e)
	}
}

// evidence prints the thread and each signal with its text; none closes
// the retro with its root cause.
func TestRetroEvidenceAndNone(t *testing.T) {
	b := dialDispatchMain(t)
	ctx := context.Background()
	roughTask(t, b, "rough")
	var out bytes.Buffer
	d := retroDeps{Bus: b, Self: "coder", Getenv: emptyEnv, Ancestor: ancestorIs(""), Master: "master"}
	if err := runRetro(ctx, d, []string{"evidence", "rough"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"task rough, owner coder, state done", "master→coder directive: build it",
		"coder→master reply: merged", "signals (1)", "reject ", "foureyes rejected coder's rough: the format check is a substring match"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("evidence lacks %q:\n%s", s, out.String())
		}
	}

	if err := runRetro(ctx, d, []string{"none", "rough", "a", "one-off", "typo"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	board, _ := b.Board(ctx)
	e := board["retro-rough"]
	if e.State != "done" {
		t.Fatalf("retro-rough %+v", e)
	}
	msgs, _ := b.Thread(ctx, e.Request.Thread)
	if last := msgs[len(msgs)-1]; last.Type != bus.CmdReply || last.Message != "no lasting lesson: a one-off typo" {
		t.Errorf("close reply %+v", last)
	}
	if err := runRetro(ctx, retroDeps{Bus: b, Getenv: emptyEnv}, []string{"none", "rough", "x"}, &bytes.Buffer{}); err == nil {
		t.Error("none without an agent: no error")
	}
	if err := runRetro(ctx, d, []string{"none", "green", "x"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "has no retro") {
		t.Errorf("none of a task without retro: %v", err)
	}
}

func TestRetroUsage(t *testing.T) {
	d := retroDeps{Getenv: emptyEnv}
	for _, args := range [][]string{nil, {"request"}, {"bogus", "x"}, {"evidence", "Bad Task"}, {"none", "x"}, {"request", "x", "y"}} {
		if err := runRetro(context.Background(), d, args, &bytes.Buffer{}); err == nil {
			t.Errorf("%q: no error", args)
		}
	}
}

// A failed friction check after done is told on stderr, never the done's
// failure; a retro not sent by this done is not reported.
func TestRetroDueLine(t *testing.T) {
	ctx := context.Background()
	var out, errOut bytes.Buffer
	fail := func(context.Context, string) (bus.Retro, error) { return bus.Retro{}, errors.New("broker gone") }
	if err := retroDueLine(ctx, fail, "x", &out, &errOut); err != nil {
		t.Errorf("failed check failed the done: %v", err)
	}
	if out.Len() != 0 || errOut.String() != "retro check of x failed: broker gone\n" {
		t.Errorf("out %q err %q", out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	existing := func(context.Context, string) (bus.Retro, error) {
		return bus.Retro{Task: "retro-x", Signals: []bus.FrictionSignal{{Kind: "reject", ID: "1-0"}}, To: "coder"}, nil
	}
	if err := retroDueLine(ctx, existing, "x", &out, &errOut); err != nil || out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("not sent: %v %q %q", err, out.String(), errOut.String())
	}
	sent := func(context.Context, string) (bus.Retro, error) {
		return bus.Retro{Task: "retro-x-2", Signals: []bus.FrictionSignal{{Kind: "reject", ID: "1-0"}}, To: "coder", Sent: true}, nil
	}
	if err := retroDueLine(ctx, sent, "x", &out, &errOut); err != nil || out.String() != "retro due: retro-x-2 (1 reject)\n" {
		t.Errorf("sent: %v %q", err, out.String())
	}
}
