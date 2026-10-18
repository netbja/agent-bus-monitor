package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func waitingShared(now time.Time) *shared {
	st := demoShared(now)
	st.agents["master"] = &agentState{state: "waiting", message: "4 decisions for Bernard", stateAt: now.Add(-time.Minute), lastSeen: now.Add(-time.Minute)}
	return st
}

func TestWaitingAgentComesFirstWithThePersonGlyph(t *testing.T) {
	now := time.Now()
	st := waitingShared(now)
	if names := sortedAgentNames(st.agents, ""); names[0] != "master" {
		t.Fatalf("order %v: a waiting agent must come first", names)
	}
	row := agentRow("master", st.agents["master"], false, TierWide, 120, now).text()
	if !strings.Contains(row, GlyphHuman) || !strings.Contains(row, "waiting") {
		t.Fatalf("row %q", row)
	}
	old := &agentState{state: "waiting", stateAt: now.Add(-48 * time.Hour), lastSeen: now.Add(-48 * time.Hour)}
	if dormant(old, false, now) {
		t.Fatal("an agent waiting for a person is never hidden as dormant")
	}
}

func TestFadeBreathesBetweenTwentyAndFullOverThreeSeconds(t *testing.T) {
	base := time.UnixMilli(1_800_000_000_000 - 1_800_000_000_000%3000)
	at := func(ms int) int { return fadeLevel(base.Add(time.Duration(ms) * time.Millisecond)) }
	if at(0) != 20 || at(1500) != 100 || at(3000) != 20 {
		t.Fatalf("fade 0/1.5/3s = %d/%d/%d", at(0), at(1500), at(3000))
	}
	if q := at(750); q <= 20 || q >= 100 {
		t.Fatalf("fade at 0.75s = %d, want in between", q)
	}
}

func TestOnlyTheWaitingWordFades(t *testing.T) {
	now := time.Now()
	st := waitingShared(now)
	row := agentRow("master", st.agents["master"], false, TierWide, 120, now)
	faded := 0
	for _, s := range row {
		if s.Fade > 0 {
			faded++
			if !strings.Contains(s.Text, "waiting") {
				t.Fatalf("faded segment %q", s.Text)
			}
		}
	}
	if faded != 1 {
		t.Fatalf("%d faded segments", faded)
	}
	if hl := headerLines(headerData{project: "p", health: health{ok: true}, asks: 1, now: now}, 160, TierWide)[0]; true {
		for _, s := range hl {
			if s.Fade > 0 || (strings.Contains(s.Text, "waiting for you") && s.Bold) {
				t.Fatalf("the header must stay still: %+v", s)
			}
		}
	}
}

func TestFadedColourRunsFromTheLineColourToTheRole(t *testing.T) {
	saved := th
	defer func() { th = saved }()
	th, _ = ResolveTheme("dark", "", "", false, 1<<24)
	if th.Faded(RoleAttention, 100) != th.colors[RoleAttention] || th.Faded(RoleAttention, 0) != th.colors[RoleLine] {
		t.Fatal("fade ends")
	}
	th, _ = ResolveTheme("dark", "", "", false, 16)
	if th.Animated() || th.Faded(RoleAttention, 20) != th.colors[RoleAttention] {
		t.Fatal("16 colours: no fade, full colour")
	}
}

func TestHeaderSaysWhoWaitsForYouAndTheBellRingsOncePerDeclaration(t *testing.T) {
	now := time.Now()
	h := newHarness(t, waitingShared(now))
	if top := strings.SplitN(h.draw(160, 45), "\n", 2)[0]; !strings.Contains(top, GlyphHuman+" 1 waiting for you") {
		t.Fatalf("header %q", top)
	}
	rings := 0
	h.u.bell = func() { rings++ }
	h.u.onTick()
	h.u.onTick()
	if rings != 1 {
		t.Fatalf("rang %d times for one declaration", rings)
	}
	h.u.st.agents["master"].stateAt = now // a new question
	h.u.onTick()
	h.u.st.agents["master"].state = "working"
	h.u.onTick()
	h.u.st.agents["master"].state = "waiting"
	h.u.onTick()
	if rings != 3 {
		t.Fatalf("rings %d, want one per new waiting declaration (3)", rings)
	}
}

func TestWaitingAnswerRequiresLaterHumanEntryAddressedToTheAgentOrItsThread(t *testing.T) {
	declared := time.UnixMilli(1_800_000_000_000)
	rootID := "1800000000001-0"
	root := bus.Event{ID: rootID, Kind: "cmd", From: "master", Target: bus.PeopleTarget, Type: bus.CmdAsk}
	cases := []struct {
		name   string
		event  bus.Event
		wantAt time.Time
	}{
		{name: "direct human", event: bus.Event{ID: "1800000000002-0", Kind: "cmd", From: "bernard", Target: "master", Human: true}, wantAt: time.UnixMilli(1_800_000_000_002)},
		{name: "all fanout", event: bus.Event{ID: "1800000000003-0", Kind: "cmd", From: "bernard", Target: "master", Human: true}, wantAt: time.UnixMilli(1_800_000_000_003)},
		{name: "thread reply", event: bus.Event{ID: "1800000000004-0", Kind: "cmd", From: "bernard", Target: bus.PeopleTarget, Type: bus.CmdReply, Ref: rootID, Human: true}, wantAt: time.UnixMilli(1_800_000_000_004)},
		{name: "not human", event: bus.Event{ID: "1800000000005-0", Kind: "cmd", From: "agent", Target: "master"}},
		{name: "wrong recipient", event: bus.Event{ID: "1800000000006-0", Kind: "cmd", From: "bernard", Target: "coder", Human: true}},
		{name: "same millisecond", event: bus.Event{ID: "1800000000000-9", Kind: "cmd", From: "bernard", Target: "master", Human: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := []bus.Event{root, tc.event}
			if got := waitingAnswerAt("master", declared, events); !got.Equal(tc.wantAt) {
				t.Fatalf("answerAt = %v, want %v", got, tc.wantAt)
			}
		})
	}
	if got := waitingAnswerAt("master", declared, []bus.Event{{ID: "1800000000007-0", Kind: "notify", From: "bernard", Human: true}}); !got.IsZero() {
		t.Fatalf("human notify answered a wait: %v", got)
	}
	if got := waitingAnswerAt("master", time.UnixMilli(1_800_000_000_003), []bus.Event{root, cases[0].event}); !got.IsZero() {
		t.Fatalf("reply before a later declaration carried forward: %v", got)
	}
}

func TestAnsweredWaitingIsDisplayOnlyAndDoesNotRingOrCount(t *testing.T) {
	now := time.Now()
	a := &agentState{state: "waiting", message: "waiting for direction", stateAt: now.Add(-10 * time.Minute), answeredAt: now.Add(-2 * time.Minute)}
	glyph, word, _, _ := stateLook(a)
	if word != "answered" || glyph == GlyphHuman || waitsForPerson(a) || waitingQuestions(map[string]*agentState{"master": a}) != 0 {
		t.Fatalf("answered state still looks like waiting: glyph=%q word=%q", glyph, word)
	}
	if len(asksFor(map[string]*agentState{"master": a})) != 0 {
		t.Fatal("answered wait is still in bell list")
	}
	row := agentRow("master", a, false, TierWide, 120, now)
	for _, seg := range row {
		if seg.Fade > 0 {
			t.Fatalf("answered row fades: %+v", seg)
		}
	}
	if got := updatedCell(a, TierNarrow, now)[0].Text; got != "2m" {
		t.Fatalf("answered age %q, want age since reply", got)
	}
	a.openAsk = 1
	if _, word, _, _ := stateLook(a); word != "waiting" {
		t.Fatalf("open ask did not reactivate waiting: %q", word)
	}
}
