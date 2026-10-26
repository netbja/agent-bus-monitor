package main

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rivo/tview"

	"github.com/netbja/agent-bus-monitor/bus"
	guardian "github.com/netbja/agent-bus-monitor/health"
)

// The overlay capture (busmon Charm spec, step a): every overlay body as the
// person reads it, tags stripped, recorded in testdata/overlay/ from the code on
// origin/main before the bodies moved off tview tags. The rewrite must reproduce
// every capture exactly; the one named exception is overlay/code-wrap.txt, whose
// change (code wrapped instead of cut) lands in its own commit.

var updateOverlays = flag.Bool("update-overlays", false, "rewrite testdata/overlay/*.txt")

// plainOf is what a body shows: a tagged string read the way the overlay
// TextView reads it, or Lines joined, so this test runs before and after the
// bodies changed type.
func plainOf(v any) string {
	switch b := v.(type) {
	case string:
		return tview.NewTextView().SetDynamicColors(true).SetText(b).GetText(true)
	case []Line:
		var sb strings.Builder
		for i, l := range b {
			if i > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(l.text())
		}
		return sb.String()
	case Line:
		return b.text()
	}
	panic("plainOf: unexpected body type")
}

// capNow is the fixed clock of every capture whose builder takes now.
var capNow = time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC)

// volatile ages, for the two captures built through the ui, which read the
// wall clock themselves (the reminder's ages).
var volatileAge = regexp.MustCompile(`\b\d+[smhd]\b`)

func longCode() string {
	return "## Done\n- adapter in `d0a72be`\n\n```go\n" +
		"func veryLongFunctionName(argumentOne string, argumentTwo int, argumentThree map[string][]string) (result string, err error) {\n" +
		"\treturn \"\", nil\n}\n```\n\n## What remains for you\n- nothing"
}

func overlayCaptures(t *testing.T) map[string]string {
	t.Helper()
	local := time.Local // bodies print local clock times: pin the zone so a capture is the same anywhere
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })
	width := mdWidth // the overlay text width is a global a relayout sets: the captures are at its default
	mdWidth = 96
	t.Cleanup(func() { mdWidth = width })
	ev := func(id, kind, typ, from, target, msg, full string) bus.Event {
		return bus.Event{ID: id, Kind: kind, Type: typ, From: from, Agent: from, Target: target, Message: msg, Full: full}
	}
	report := ev("1792999990000-0", "report", "", "coder", "", "Lot 1 delivered", "Lot 1 delivered\n\n"+longCode())
	directive := ev("1792999991000-0", "cmd", bus.CmdDirective, "master", "coder", "review [red]lot 1[-] please", "")
	directive.Ref = "1792999980000-0"
	thread := threadState{ID: directive.Ref, Loaded: true, Entries: []bus.Event{
		ev("1792999980000-0", "cmd", bus.CmdDirective, "bernard", "master", "start lot 1", ""), directive}}

	reqs := boardRequests(map[string]bus.BoardEntry{"task-20": requestedEntry()})
	threads := openThreads([]bus.Event{
		{ID: "200-0", Kind: "cmd", Type: bus.CmdDirective, From: "hermes", Target: "coder", Message: "look at the flake"},
	}, reqs)

	board := map[string]bus.BoardEntry{"pr-46-review": {Owner: "grok", State: "blocked", Branch: "fix/pr-46"},
		"other": {Owner: "kimi", State: "working"}}
	feed := []feedLine{{ev: report, at: detNow.Add(-2 * time.Minute)}, {ev: directive, at: detNow.Add(-time.Minute)}}
	_, agentBody := agentDetail("grok", richAgent(), true, board, feed, detNow)
	_, emptyAgent := agentDetail("ghost", &agentState{}, false, nil, nil, detNow)
	_, taskBody, _ := taskDetail(board["pr-46-review"], "pr-46-review", detNow)
	_, reqBody, _ := requestDetail(reqs[0], capNow)

	p := hostilePlan(capNow)
	planBody, _, _ := planOverlay(p, 0, true, capNow, 100)
	_, planTask, _ := planTaskDetail(p, p.rows[0].Task.ID, capNow)

	b := bus.BudgetSnapshot{Windows: []bus.BudgetWindow{
		{Name: "weekly", UsedPct: 76, ResetsAt: capNow.Add(72 * time.Hour).Unix(), Minutes: 10080, ObservedAt: capNow.UnixMilli()},
		{Name: "session", UsedPct: 6, ResetsAt: capNow.Add(time.Hour).Unix(), Minutes: 300, ObservedAt: capNow.UnixMilli()},
	}}

	inst := &bus.ServicesInstall{Kinds: []string{"dispatch", "missions"}, Manager: "systemd", Host: "here"}
	recs := map[string]bus.ServiceRecord{
		"dispatch": {State: "failed", Detail: "restarting", Error: "broker went away", At: capNow.Add(-5 * time.Second).UnixMilli(), PID: 7, Host: "here", Log: "/nowhere/dispatch.log"},
		"missions": {State: "idle", Detail: "waiting for missions", At: capNow.UnixMilli(), Host: "elsewhere", Log: "/far/missions.log"},
	}
	health := healthOverlayText(map[string]guardian.Record{
		"tracked-secrets": {ID: "tracked-secrets", Status: guardian.Fail, Detail: "tracked private-key path: [candidate]", ObservedAt: capNow.UnixMilli(), Since: capNow.UnixMilli()},
	}, map[string]guardian.Note{}, capNow)

	out := map[string]string{
		"message-report":    plainOf(detailText(report, capNow.Add(-time.Minute), capNow, threadState{})),
		"message-directive": plainOf(detailText(directive, capNow.Add(-time.Minute), capNow, thread)),
		"agent":             plainOf(agentBody),
		"agent-empty":       plainOf(emptyAgent),
		"task":              plainOf(taskBody),
		"request":           plainOf(reqBody),
		"requests":          plainOf(requestsPanel(reqs, threads, capNow, 80)),
		"requests-empty":    plainOf(requestsPanel(nil, nil, capNow, 80)),
		"plan":              plainOf(planBody),
		"plan-task":         plainOf(planTask),
		"quota":             plainOf(quotaDetail("openai", b, true, capNow.Add(time.Hour), true, capNow)),
		"quota-none":        plainOf(quotaDetail("", b, false, time.Time{}, false, capNow)),
		"services":          plainOf(servicesPanel(inst, recs, "here", capNow, 100)),
		"health":            plainOf(health),
		"help":              plainOf(helpText()),
		"ask":               plainOf(askPanelBody(demoAsk(), askPanel{q: 1, opt: 0}, 70)),
		"ask-narrow":        plainOf(askPanelBody(demoAsk(), askPanel{q: 2}, 30)),
		"code-wrap":         plainOf(renderMarkdown(longCode(), 60)),
	}

	// Bodies built inside the ui: the reminder, the recap and the summary confirmation.
	h := newHarness(t, demoShared(capNow))
	h.u.cachePath = filepath.Join(t.TempDir(), "cache.json")
	h.u.cache = &busmonCache{Project: "demo", Snoozes: map[string]int64{}}
	h.u.openReminder([]attentionItem{
		{Key: "wait:new:2", Agent: "new", Text: "new [red]one[-]", At: time.Now().Add(-6 * time.Minute)},
		{Key: "wait:old:1", Agent: "old", Text: "old", At: time.Now().Add(-10 * time.Minute)},
	})
	out["reminder"] = volatileAge.ReplaceAllString(h.u.overlay.GetText(true), "<age>")
	h.u.closeOverlay()

	h = newHarness(t, demoShared(capNow))
	h.u.recap = buildRecap(recapCaptureInput())
	h.u.recapPending = true
	h.u.maybeOpenRecap()
	out["recap"] = h.u.overlay.GetText(true)
	h.u.closeOverlay()
	h.u.recap = recapModel{Target: "master"}
	h.u.openSummaryConfirmation()
	out["summary-confirm"] = h.u.overlay.GetText(true)
	return out
}

func recapCaptureInput() recapInput {
	now := time.UnixMilli(1_800_000_000_000)
	return recapInput{
		Now: now, Since: now.Add(-time.Hour), Human: "bernard",
		Agents: map[string]*agentState{
			"master": {state: "working", message: "coordinating", stateAt: now.Add(-5 * time.Minute), registered: true, provider: "anthropic"},
			"coder":  {state: "waiting", message: "review [red]this[-]", stateAt: now.Add(-30 * time.Minute), registered: true},
		},
		Asks:  []bus.Ask{{ID: "ask-1", From: "coder", State: "open", Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "q1", Header: "Review", Question: "Approve this?"}}}}},
		Board: map[string]bus.BoardEntry{"first": {State: "open", Owner: "master", Branch: "main"}},
		Commands: []bus.Event{
			{ID: "1799999990000-0", Kind: "cmd", From: "coder", Target: bus.PeopleTarget, Type: bus.CmdDirective, Message: "Need a decision"},
			{ID: "1799999992000-0", Kind: "cmd", From: "dispatch", Target: "master", Type: bus.CmdComplete, Message: "change integrated"},
		},
		Verdicts: []bus.Verdict{{ID: "1799999996000-0", Subject: "pr:7", Author: "coder", Reviewer: "foureyes", Decision: "approve", TS: 1_799_999_996_000}},
		Services: map[string]bus.ServiceRecord{"dispatch": {State: "failed", Error: "exited", At: 1_799_999_998_000}},
		Holds:    map[string]int64{},
	}
}

func TestOverlayCaptures(t *testing.T) {
	dir := filepath.Join("testdata", "overlay")
	for name, got := range overlayCaptures(t) {
		path := filepath.Join(dir, name+".txt")
		if *updateOverlays {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run go test -run TestOverlayCaptures -update-overlays on the capture commit)", name, err)
		}
		if got != string(want) {
			t.Errorf("%s changed:\n--- want\n%s\n--- got\n%s", name, want, got)
		}
	}
}
