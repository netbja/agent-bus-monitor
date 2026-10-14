package main

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func letter(r rune) *tcell.EventKey { return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone) }

func dormantShared(now time.Time) *shared {
	st := demoShared(now)
	st.agents["claude-hl-master"] = &agentState{state: "working", message: "old team",
		stateAt: now.Add(-20 * 24 * time.Hour), lastSeen: now.Add(-20 * 24 * time.Hour)}
	return st
}

func TestAgentsPanelHidesDormantAgentsUntilA(t *testing.T) {
	h := newHarness(t, dormantShared(time.Now()))
	screen := h.draw(160, 45)
	if strings.Contains(screen, "claude-h") || !strings.Contains(screen, "1 dormant (a shows)") {
		t.Fatalf("dormant agent shown or not counted:\n%s", screen)
	}
	h.key(letter('a'))
	if screen = h.draw(160, 45); !strings.Contains(screen, "claude-h") || !strings.Contains(screen, "a hides dormant") {
		t.Fatalf("a did not show it:\n%s", screen)
	}
	if names := h.u.agentNames(); strings.Contains(strings.Join(names, ","), "claude-hl") {
		t.Fatalf("@all reaches a dormant agent: %v", names)
	}
}

func TestDeleteForgetsADormantAgentAfterY(t *testing.T) {
	h := newHarness(t, dormantShared(time.Now()))
	h.key(letter('a'))
	h.draw(160, 45)
	h.u.lists[PanelAgents].sel = "claude-hl-master"
	h.key(tcell.NewEventKey(tcell.KeyDelete, 0, tcell.ModNone))
	if h.u.confirm == nil || !strings.Contains(h.u.flash, "forget agent claude-hl-master") {
		t.Fatalf("no confirmation asked: %q", h.u.flash)
	}
	h.key(letter('y'))
	if got := strings.Join(h.w.sent, "|"); got != "forget claude-hl-master" {
		t.Fatalf("sent %q", got)
	}
	if _, ok := h.u.st.agents["claude-hl-master"]; ok {
		t.Fatal("still in the panel")
	}
}

func TestDeleteRefusesALiveAgentAndAnyOtherKeyCancels(t *testing.T) {
	h := newHarness(t, dormantShared(time.Now()))
	h.draw(160, 45)
	h.u.lists[PanelAgents].sel = "foureyes" // declared 30s ago
	h.key(letter('d'))
	if h.u.confirm != nil || !strings.Contains(h.u.flash, "not removed") {
		t.Fatalf("a live agent was offered for removal: %q", h.u.flash)
	}
	h.key(letter('a'))
	h.u.lists[PanelAgents].sel = "claude-hl-master"
	h.key(letter('d'))
	h.key(letter('n'))
	if len(h.w.sent) != 0 || h.u.confirm != nil || h.u.flash != "not removed" {
		t.Fatalf("cancel: sent %v, flash %q", h.w.sent, h.u.flash)
	}
}

func TestDeleteClosesAnOpenThreadWithAReply(t *testing.T) {
	st := dormantShared(time.Now())
	st.threads = []openThread{{ID: "1-0", Type: "directive", From: "master", Target: "claude-hl-coder", At: time.Now().Add(-time.Hour)}}
	h := newHarness(t, st)
	h.draw(160, 45)
	h.u.setFocusPanel(PanelRequests)
	h.u.lists[PanelRequests].sel = "thr:1-0"
	h.key(letter('d'))
	h.key(letter('y'))
	if got := strings.Join(h.w.sent, "|"); got != "close master 1-0" || len(h.u.st.threads) != 0 {
		t.Fatalf("sent %q, threads %v", got, h.u.st.threads)
	}
}

func TestTodaysSeparatorSaysToday(t *testing.T) {
	now := time.Date(2026, 10, 14, 22, 0, 0, 0, time.Local)
	todayFunc = func() time.Time { return now }
	t.Cleanup(func() { todayFunc = time.Now })
	today := activityRow(feedLine{sep: true, at: now.Add(-time.Hour)}, TierWide, 60)
	yesterday := activityRow(feedLine{sep: true, at: now.Add(-24 * time.Hour)}, TierWide, 60)
	if today.Width() != 60 || yesterday.Width() != 60 {
		t.Fatalf("widths %d %d", today.Width(), yesterday.Width())
	}
	found := false
	for _, s := range today {
		found = found || (s.Text == "(today)" && s.Role == RoleOK)
	}
	if !found {
		t.Fatalf("today = %+v", today)
	}
	for _, s := range yesterday {
		if strings.Contains(s.Text, "today") {
			t.Fatalf("yesterday = %+v", yesterday)
		}
	}
}

func TestTodaysSeparatorReadsDateDashToday(t *testing.T) {
	now := time.Date(2026, 10, 14, 22, 0, 0, 0, time.Local)
	todayFunc = func() time.Time { return now }
	t.Cleanup(func() { todayFunc = time.Now })
	got := activityRow(feedLine{sep: true, at: now}, TierWide, 40).text()
	if !strings.Contains(got, " Wed 2026-10-14 - (today) ") {
		t.Fatalf("separator %q", got)
	}
}

func TestFTogglesAutoScroll(t *testing.T) {
	h := newHarness(t, demoShared(time.Now()))
	h.draw(160, 45)
	act := h.u.lists[PanelActivity]
	if !act.Live() {
		t.Fatal("activity should start live")
	}
	h.key(letter('f'))
	if act.Live() || act.Selected() != "" || !strings.Contains(h.draw(160, 45), "auto-scroll off") {
		t.Fatalf("f did not pause: live %v sel %q", act.Live(), act.Selected())
	}
	h.key(letter('f'))
	if !act.Live() || !strings.Contains(h.draw(160, 45), "auto-scroll on") {
		t.Fatal("f did not resume")
	}
}
