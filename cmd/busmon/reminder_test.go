package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/netbja/agent-bus-monitor/bus"
)

func TestAttentionItemsAreStableAndDistinct(t *testing.T) {
	now := time.Now()
	asks := []bus.Ask{{ID: "ask-2", From: "coder", State: "open", CreatedAt: now.Add(-10 * time.Minute).UnixMilli(), Questions: []bus.AskQuestionState{
		{AskQuestion: bus.AskQuestion{ID: "q1", Question: "Choose"}},
		{AskQuestion: bus.AskQuestion{ID: "q2", Question: "Explain"}, Answer: &bus.AskAnswer{By: "bernard"}},
	}}}
	agents := map[string]*agentState{
		"coder": {state: "waiting", message: "need review", stateAt: now.Add(-20 * time.Minute)},
		"done":  {state: "waiting", stateAt: now.Add(-time.Hour), answeredAt: now.Add(-30 * time.Minute)},
	}
	items := attentionItems(agents, asks)
	if len(items) != 2 || items[0].Key != "wait:coder:"+strconv.FormatInt(agents["coder"].stateAt.UnixMilli(), 10) || items[1].Key != "ask:ask-2:q1" {
		t.Fatalf("attention items not oldest-first / distinct: %+v", items)
	}
}

func TestDueAttentionHonoursThresholdAndSnooze(t *testing.T) {
	now := time.Now()
	items := []attentionItem{{Key: "wait:master:1", At: now.Add(-6 * time.Minute)}}
	cache := &busmonCache{Snoozes: map[string]int64{}}
	if got := dueAttention(items, cache, now, 5*time.Minute); len(got) != 1 {
		t.Fatalf("due reminders %v, want one", got)
	}
	cache.snooze(items[0].Key, now.Add(15*time.Minute))
	if got := dueAttention(items, cache, now, 5*time.Minute); len(got) != 0 {
		t.Fatalf("snoozed reminders %v", got)
	}
	if got := dueAttention(items, cache, now.Add(16*time.Minute), 5*time.Minute); len(got) != 1 {
		t.Fatalf("expired snooze reminders %v, want one", got)
	}
	if got := dueAttention(items, cache, now, 0); len(got) != 0 {
		t.Fatalf("disabled popup reminders %v", got)
	}
}

func TestReminderWaitsForComposeDraftAndOtherModal(t *testing.T) {
	now := time.Now()
	st := &shared{agents: map[string]*agentState{}, asks: []bus.Ask{{ID: "ask-1", From: "coder", State: "open", CreatedAt: now.Add(-10 * time.Minute).UnixMilli(), Questions: []bus.AskQuestionState{{AskQuestion: bus.AskQuestion{ID: "q1", Question: "Review?"}}}}}}
	h := newHarness(t, st)
	h.u.settings.PopupAfter = time.Minute
	h.u.cache = &busmonCache{Project: "demo", Snoozes: map[string]int64{}}
	h.u.msgDraft = "draft"
	h.u.maybeReminder()
	if h.u.ov != ovNone {
		t.Fatal("reminder covered a line draft")
	}
	h.u.msgDraft = ""
	h.u.comp.open = true
	h.u.maybeReminder()
	if h.u.ov != ovNone {
		t.Fatal("reminder covered the compose window")
	}
	h.u.comp.open = false
	h.u.showOverlay(ovHelp, "Help", []Line{{S(RoleText, "help")}}, "", "")
	h.u.maybeReminder()
	if h.u.ov != ovHelp {
		t.Fatal("reminder covered another modal")
	}
	h.u.closeOverlay()
	if h.u.ov != ovReminder {
		t.Fatalf("queued reminder did not open when modal cleared: %v", h.u.ov)
	}
}

func TestReminderDisplaysOldestFirstAndEscSnoozesBatch(t *testing.T) {
	now := time.Now()
	h := newHarness(t, &shared{agents: map[string]*agentState{}})
	h.u.settings.Snooze = 15 * time.Minute
	h.u.cachePath = filepath.Join(t.TempDir(), "cache.json")
	h.u.cache = &busmonCache{Project: "demo", Snoozes: map[string]int64{}}
	items := []attentionItem{
		{Key: "wait:new:2", Agent: "new", Text: "new", At: now.Add(-6 * time.Minute)},
		{Key: "wait:old:1", Agent: "old", Text: "old", At: now.Add(-10 * time.Minute)},
	}
	h.u.openReminder(items)
	items = []attentionItem{items[0], items[1]} // intentionally newest first
	h.u.openReminder(items)
	if h.u.reminders[0].Agent != "old" || !strings.Contains(h.u.overlay.GetText(false), "old") || h.u.reminderIndex != 0 {
		t.Fatalf("reminder body %q index %d", h.u.overlay.GetText(false), h.u.reminderIndex)
	}
	h.u.handleKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	for _, item := range items {
		if !h.u.cache.snoozed(item.Key, time.Now()) {
			t.Fatalf("%s was not snoozed", item.Key)
		}
	}
	b, err := os.ReadFile(h.u.cachePath)
	if err != nil || !strings.Contains(string(b), "snoozes") {
		t.Fatalf("cache not saved: %v %s", err, b)
	}
}

func TestBusmonCacheWritesAtomicallyWith0600Mode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "demo.json")
	c := &busmonCache{Project: "demo", LastVisit: 123, Snoozes: map[string]int64{"x": 456}}
	if err := c.save(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("cache mode %o, want 600", info.Mode().Perm())
	}
	if _, err := loadBusmonCache(path, "demo"); err != nil {
		t.Fatal(err)
	}
}
