package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestAskCLIRoundTrip(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { r.Del(ctx, bus.AsksKey(b.Project())) })
	t.Setenv("HERDR_ENV", "")
	in := `{"title":"Opening","id":"forged","questions":[
	  {"id":"date","header":"Date T","question":"When?","answer":{"choice":"Mon"},
	   "options":[{"label":"Mon","recommended":true},{"label":"Wed"}]}]}`
	var out, errw bytes.Buffer
	if code := runAsk(ctx, b, []string{"master"}, strings.NewReader(in), &out, &errw); code != 0 {
		t.Fatalf("ask: %d %s", code, errw.String())
	}
	id := strings.Fields(out.String())[0]
	if id == "forged" {
		t.Fatal("the client id was used")
	}
	snaps, _ := b.Agents(ctx)
	if snaps["master"].State != "waiting" || !strings.Contains(snaps["master"].Message, "1 question(s): Opening") {
		t.Fatalf("status %+v", snaps["master"])
	}
	asks, _ := b.Asks(ctx)
	if len(asks) != 1 || asks[0].Questions[0].Answer != nil {
		t.Fatalf("a forged answer was stored: %+v", asks)
	}
	out.Reset()
	if code := runAnswer(ctx, b, []string{id, "date", "--choice", "Mon", "--as", "Bernard"}, func(string) string { return "" }, &out, &errw); code != 0 {
		t.Fatalf("answer: %d %s", code, errw.String())
	}
	out.Reset()
	runAsks(ctx, b, nil, &out, &errw)
	if !strings.Contains(out.String(), "answered") || !strings.Contains(out.String(), "Mon (bernard)") {
		t.Fatalf("asks:\n%s", out.String())
	}
	if code := runAsk(ctx, b, []string{"master"}, strings.NewReader(`{"title":"x","questions":[]}`), &out, &errw); code == 0 {
		t.Fatal("an ask with no question was accepted")
	}
}

func TestAskCLITask(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { r.Del(ctx, bus.AsksKey(b.Project())) })
	t.Setenv("HERDR_ENV", "")
	in := `{"title":"Merge roles?","questions":[{"id":"go","header":"Merge","question":"Merge?","options":[{"label":"yes"},{"label":"no"}]}]}`
	var out, errw bytes.Buffer
	if code := runAsk(ctx, b, []string{"master", "--task", "roles"}, strings.NewReader(in), &out, &errw); code != 0 {
		t.Fatalf("ask: %d %s", code, errw.String())
	}
	asks, _ := b.Asks(ctx)
	if len(asks) != 1 || asks[0].Task != "roles" {
		t.Fatalf("task not stored: %+v", asks)
	}
	if code := runAsk(ctx, b, []string{"master", "--task", "Bad Task", "--replace", asks[0].ID}, strings.NewReader(in), &out, &errw); code == 0 {
		t.Fatal("an invalid task was accepted")
	}
}
