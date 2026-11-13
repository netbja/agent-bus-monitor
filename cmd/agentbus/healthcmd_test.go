package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

func TestHealthCommandListDetailAndNote(t *testing.T) {
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r, "healthcmd"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()); _ = r.Close() })
	if err := b.MarkServicesInstalled(context.Background(), bus.ServicesInstall{Kinds: []string{"dispatch"}, Manager: "test"}); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := runHealth(context.Background(), b, "sentinel", []string{"list"}, &out, &errw); code != 0 {
		t.Fatalf("list code=%d err=%s", code, errw.String())
	}
	if !strings.Contains(out.String(), "services") || !strings.Contains(out.String(), "unknown") {
		t.Fatalf("list output=%q", out.String())
	}
	out.Reset()
	if code := runHealth(context.Background(), b, "sentinel", []string{"note", "services", "checked safely"}, &out, &errw); code != 0 {
		t.Fatalf("note code=%d err=%s", code, errw.String())
	}
	out.Reset()
	if code := runHealth(context.Background(), b, "sentinel", []string{"detail", "services"}, &out, &errw); code != 0 {
		t.Fatalf("detail code=%d err=%s", code, errw.String())
	}
	if !strings.Contains(out.String(), "remediation:") || !strings.Contains(out.String(), "checked safely") {
		t.Fatalf("detail output=%q", out.String())
	}
}

// health <check>... (agent-brief): only those checks, each with its status,
// reason and detail lines; an unknown id is a usage error.
func TestHealthCommandNamedChecks(t *testing.T) {
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r, "healthcmd"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()); _ = r.Close() })
	if err := b.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}, Manager: "test"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.AcquireDispatch(ctx, "h", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	rec := health.Record{ID: "team-stall", Status: health.Warn, Reason: "waiting-person", Detail: "next ready: tag (person)", Fingerprint: "f"}
	if err := b.FencedHealthPut(ctx, "h", "master", rec); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := runHealth(ctx, b, "coder", []string{"team-stall", "services"}, &out, &errw); code != 0 {
		t.Fatalf("code=%d err=%s", code, errw.String())
	}
	want := "team-stall warn\n  reason: waiting-person\n  detail: next ready: tag (person)\nservices unknown\n  detail: no current measurement\n"
	if out.String() != want {
		t.Fatalf("output %q\nwant   %q", out.String(), want)
	}
	out.Reset()
	if code := runHealth(ctx, b, "coder", []string{"team-stall", "nope"}, &out, &errw); code != 2 || out.Len() != 0 {
		t.Fatalf("unknown check: code=%d out=%q", code, out.String())
	}
}

// health warns (sentinel's daily digest) and health accept (the person's):
// a warn of 50 h is listed with its age; accepted, it is held back and
// counted; an accept needs --as and no agent identity.
func TestHealthWarnsAndAccept(t *testing.T) {
	b, r := dialMain(t)
	ctx := context.Background()
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()) })
	if err := b.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}, Manager: "test"}); err != nil {
		t.Fatal(err)
	}
	run := func(self string, args ...string) (int, string, string) {
		var out, errw bytes.Buffer
		code := runHealth(ctx, b, self, args, &out, &errw)
		return code, out.String(), errw.String()
	}
	if code, out, _ := run("sentinel", "warns"); code != 0 || !strings.Contains(out, "no check in warn over 24 h") {
		t.Fatalf("nothing: %d %q", code, out)
	}
	now, _ := b.ServerTime(ctx)
	rec := health.Record{ID: "disk", Status: health.Warn, Detail: "data at 91%", Since: now.Add(-50 * time.Hour).UnixMilli(), ObservedAt: now.UnixMilli(), Fingerprint: "f"}
	raw, _ := json.Marshal(rec)
	if err := r.HSet(ctx, bus.HealthKey(b.Project()), "disk", raw).Err(); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run("sentinel", "warns")
	if code != 0 || !strings.HasPrefix(out, "Health digest: 1 checks in warn over 24 h") || !strings.Contains(out, "- disk warn 2d2h: data at 91%") {
		t.Fatalf("a long warn: %d %q", code, out)
	}
	for _, c := range []struct {
		self string
		args []string
	}{
		{"anonymous", []string{"accept", "disk", "grows on purpose"}},      // no --as
		{"coder", []string{"accept", "disk", "--as", "charlie", "x"}},      // an agent's
		{"anonymous", []string{"accept", "nope", "--as", "charlie", "x"}},  // no such check
		{"anonymous", []string{"accept", "disk", "--as", "charlie"}},       // no reason
		{"anonymous", []string{"accept", "disk", "--as", "Bad Name", "x"}}, // not a name
	} {
		if code, _, _ := run(c.self, c.args...); code == 0 {
			t.Errorf("%s %v accepted", c.self, c.args)
		}
	}
	if code, out, errw := run("anonymous", "accept", "disk", "--as", "charlie", "the", "volume", "grows"); code != 0 || !strings.Contains(out, "accepted") {
		t.Fatalf("accept: %d %q %q", code, out, errw)
	}
	if code, out, _ := run("sentinel", "warns"); code != 0 || !strings.Contains(out, "no check in warn over 24 h (1 accepted by the person this week)") {
		t.Fatalf("accepted: %d %q", code, out)
	}
}

// The sentinel skill's daily review sends the long warns as one digest and
// never accepts a warn; every `agentbus health <sub>` it names exists.
func TestSentinelSkillSendsOneWarnDigest(t *testing.T) {
	raw, err := os.ReadFile("../../skills/agent-bus-sentinel/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	skill := string(raw)
	for _, want := range []string{"agentbus health warns", "**one** message", "never one message per warn",
		"You never accept a warn yourself", "your\nallow-list is unchanged"} {
		if !strings.Contains(skill, want) {
			t.Errorf("the skill lacks %q", want)
		}
	}
	var usage string
	for _, c := range commands {
		if c.Name == "health" {
			usage = strings.Join(c.Usage, " ")
		}
	}
	for _, m := range regexp.MustCompile("agentbus health ([a-z]+)").FindAllStringSubmatch(skill, -1) {
		if _, isCheck := health.ByID(m[1]); !isCheck && !strings.Contains(usage, m[1]) {
			t.Errorf("the skill names agentbus health %s, not in the help table", m[1])
		}
	}
}
