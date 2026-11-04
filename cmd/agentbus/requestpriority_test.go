package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// person runs the request priority checks as a person's shell: no
// AGENT_BUS_AGENT, no agent among its ancestors.
func person(t *testing.T) {
	t.Helper()
	old := requestDeps
	requestDeps = requestCallerDeps{Getenv: func(string) string { return "" },
		AgentCaller: func(context.Context) (string, error) { return "", nil }}
	t.Cleanup(func() { requestDeps = old })
}

func TestRequestPriorityCLI(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	defer r.Del(ctx, bus.BoardKey(b.Project()))
	person(t)
	if _, err := b.Request(ctx, "rq", "master", "coder", "", "do it", 0); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runRequest(ctx, b, "anonymous", append([]string{"priority"}, args...), &out)
		return out.String(), err
	}
	if out, err := run("rq", "high", "--as", "charlie"); err != nil || out != "rq: high (was normal)\n" {
		t.Fatalf("high: %q, %v", out, err)
	}
	if out, err := run("rq", "high", "--as", "charlie"); err != nil || out != "rq: already high\n" {
		t.Fatalf("again: %q, %v", out, err)
	}
	if out, err := run("rq", "normal", "--as", "charlie"); err != nil || out != "rq: normal (was high)\n" {
		t.Fatalf("normal: %q, %v", out, err)
	}
	for _, bad := range [][]string{{"rq", "high"}, {"rq", "urgent", "--as", "charlie"}, {"rq", "--as", "charlie"}, {"rq", "high", "--as", "Not A Name"}} {
		if _, err := run(bad...); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
	if _, err := run("nope", "high", "--as", "charlie"); err == nil || !strings.Contains(err.Error(), "unknown request") {
		t.Fatalf("unknown: %v", err)
	}
}

// Only a person: an agent's environment or an agent among the ancestors is
// refused before anything is written, and a failed check refuses too.
func TestRequestPriorityRefusesAnAgent(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	defer r.Del(ctx, bus.BoardKey(b.Project()))
	if _, err := b.Request(ctx, "rq", "master", "coder", "", "do it", 0); err != nil {
		t.Fatal(err)
	}
	old := requestDeps
	defer func() { requestDeps = old }()
	for name, d := range map[string]requestCallerDeps{
		"AGENT_BUS_AGENT": {Getenv: func(k string) string {
			if k == "AGENT_BUS_AGENT" {
				return "master"
			}
			return ""
		}, AgentCaller: func(context.Context) (string, error) { return "", nil }},
		"an agent ancestor": {Getenv: func(string) string { return "" }, AgentCaller: func(context.Context) (string, error) { return "master", nil }},
		"a failed check":    {Getenv: func(string) string { return "" }, AgentCaller: func(context.Context) (string, error) { return "", errors.New("broker down") }},
	} {
		requestDeps = d
		err := runRequest(ctx, b, "master", []string{"priority", "rq", "high", "--as", "charlie"}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "the person's act") && !strings.Contains(err.Error(), "cannot check") {
			t.Fatalf("%s: %v", name, err)
		}
	}
	board, _ := b.Board(ctx)
	if p := board["rq"].Request.Priority; p != "" {
		t.Fatalf("an agent set %q", p)
	}
}

func TestRequestNextCLI(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	defer r.Del(ctx, bus.BoardKey(b.Project()))
	person(t)
	var out bytes.Buffer
	if err := runRequest(ctx, b, "coder", []string{"next"}, &out); !errors.Is(err, errNothingReady) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := b.Request(ctx, "older", "master", "coder", "", "first", 0); err != nil {
		t.Fatal(err)
	}
	id, err := b.Request(ctx, "urgent", "master", "coder", "", "second", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetRequestPriority(ctx, "urgent", "high", "charlie"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runRequest(ctx, b, "coder", []string{"next"}, &out); err != nil || out.String() != "urgent  high  "+id+"\n" {
		t.Fatalf("next: %q, %v", out.String(), err)
	}
	out.Reset()
	if err := runRequest(ctx, b, "master", []string{"next", "coder", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var got struct{ Task, Priority, Request, Agent, Why string }
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Task != "urgent" || got.Priority != "high" || got.Request != id || got.Agent != "coder" {
		t.Fatalf("json %q: %+v %v", out.String(), got, err)
	}
	if err := b.AcceptRequest(ctx, "older", "coder"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runRequest(ctx, b, "coder", []string{"next"}, &out); err != nil || !strings.HasPrefix(out.String(), "older  normal  ") ||
		!strings.Contains(out.String(), "(finish first)") {
		t.Fatalf("in hand: %q, %v", out.String(), err)
	}
}

// The person's request priority (--as) and request next pass the identity
// guard in a herdr pane; a priority without --as does not.
func TestRequestPriorityIdentityGuard(t *testing.T) {
	pane := func(k string) string {
		if k == "HERDR_PANE_ID" {
			return "p1"
		}
		return ""
	}
	if err := identityGuard(pane, "request", []string{"priority", "rq", "high", "--as", "charlie"}); err != nil {
		t.Fatalf("the person's priority: %v", err)
	}
	if err := identityGuard(pane, "request", []string{"next", "coder"}); err != nil {
		t.Fatalf("next: %v", err)
	}
	if err := identityGuard(pane, "request", []string{"priority", "rq", "high"}); err == nil {
		t.Fatal("a priority without --as passed")
	}
	if err := identityGuard(pane, "request", []string{"accept", "rq"}); err == nil {
		t.Fatal("accept passed")
	}
}
