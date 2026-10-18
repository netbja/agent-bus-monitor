package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/netbja/agent-bus-monitor/bus"
)

type attentionItem struct {
	Key      string
	Agent    string
	Text     string
	At       time.Time
	AskID    string
	Question string
}

func attentionItems(agents map[string]*agentState, asks []bus.Ask) []attentionItem {
	var out []attentionItem
	for _, a := range asks {
		if a.State != "open" {
			continue
		}
		created := time.UnixMilli(a.CreatedAt)
		if a.CreatedAt <= 0 {
			continue
		}
		for _, q := range a.Questions {
			if q.Answer != nil {
				continue
			}
			at := created
			if at.IsZero() {
				continue
			}
			out = append(out, attentionItem{Key: "ask:" + a.ID + ":" + q.ID, Agent: a.From,
				Text: q.Question, At: at, AskID: a.ID, Question: q.ID})
		}
	}
	for name, a := range agents {
		if a.state != "waiting" || answered(a) || a.stateAt.IsZero() {
			continue
		}
		out = append(out, attentionItem{Key: fmt.Sprintf("wait:%s:%d", name, a.stateAt.UnixMilli()),
			Agent: name, Text: a.message, At: a.stateAt})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		if out[i].Agent != out[j].Agent {
			return out[i].Agent < out[j].Agent
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func dueAttention(items []attentionItem, cache *busmonCache, now time.Time, popupAfter time.Duration) []attentionItem {
	if popupAfter <= 0 {
		return nil
	}
	var out []attentionItem
	for _, item := range items {
		if item.At.IsZero() || now.Sub(item.At) < popupAfter || cache.snoozed(item.Key, now) {
			continue
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		if out[i].Agent != out[j].Agent {
			return out[i].Agent < out[j].Agent
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func (u *ui) maybeReminder() {
	if u.settings.PopupAfter <= 0 || u.recapLoading || u.recapPending || u.ov != ovNone || u.comp.open || u.composing != ModePanel ||
		strings.TrimSpace(u.msgDraft) != "" || strings.TrimSpace(u.input.GetText()) != "" || u.askChoose != nil {
		return
	}
	u.st.mu.Lock()
	items := attentionItems(u.st.agents, u.st.asks)
	u.st.mu.Unlock()
	items = dueAttention(items, u.cache, time.Now(), u.settings.PopupAfter)
	if len(items) == 0 {
		return
	}
	u.openReminder(items)
}

func (u *ui) openReminder(items []attentionItem) {
	if u.cache == nil {
		u.cache = &busmonCache{Project: u.project, Snoozes: map[string]int64{}}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].At.Equal(items[j].At) {
			return items[i].At.Before(items[j].At)
		}
		if items[i].Agent != items[j].Agent {
			return items[i].Agent < items[j].Agent
		}
		return items[i].Key < items[j].Key
	})
	u.reminders = append(u.reminders[:0], items...)
	u.reminderIndex = 0
	now := time.Now()
	for _, item := range items {
		u.cache.snooze(item.Key, now.Add(u.settings.Snooze))
	}
	if u.cachePath != "" && u.cache.save(u.cachePath) != nil {
		u.st.mu.Lock()
		u.st.configNote = joinNote(u.st.configNote, "reminder snooze not saved")
		u.st.mu.Unlock()
	}
	u.showOverlay(ovReminder, "", "", "", "")
	u.renderReminder()
}

func (u *ui) renderReminder() {
	var b strings.Builder
	for i, item := range u.reminders {
		marker := "  "
		if i == u.reminderIndex {
			marker = tg(RoleAccent, "> ")
		}
		b.WriteString(marker)
		b.WriteString(tg(RoleAttention, Preview(item.Agent)+" "+GlyphNone+" "+humanAge(max(0, time.Since(item.At)))))
		b.WriteByte('\n')
		b.WriteString("    ")
		b.WriteString(tg(RoleText, Preview(item.Text)))
		b.WriteString("\n\n")
	}
	u.overlay.SetTitle(" " + tg(RoleText, fmt.Sprintf("Waiting for you %s %d", GlyphNone, len(u.reminders))) +
		"  " + tg(RoleMuted, GlyphUp+GlyphDownArrow+" select "+GlyphNone+" Enter answer "+GlyphNone+" i message "+GlyphNone+" Esc later") + " ")
	u.overlay.SetText(strings.TrimRight(b.String(), "\n"))
	u.overlay.ScrollToBeginning()
}

func (u *ui) reminderKey(ev *tcell.EventKey) *tcell.EventKey {
	if len(u.reminders) == 0 {
		return nil
	}
	switch {
	case ev.Key() == tcell.KeyEscape:
		now := time.Now()
		for _, item := range u.reminders {
			u.cache.snooze(item.Key, now.Add(u.settings.Snooze))
		}
		if u.cachePath != "" && u.cache.save(u.cachePath) != nil {
			u.st.mu.Lock()
			u.st.configNote = joinNote(u.st.configNote, "reminder snooze not saved")
			u.st.mu.Unlock()
		}
		u.closeOverlay()
	case ev.Key() == tcell.KeyUp || ev.Key() == tcell.KeyBacktab || ev.Key() == tcell.KeyRune && ev.Rune() == 'k':
		u.reminderIndex = (u.reminderIndex + len(u.reminders) - 1) % len(u.reminders)
		u.renderReminder()
	case ev.Key() == tcell.KeyDown || ev.Key() == tcell.KeyTab || ev.Key() == tcell.KeyRune && ev.Rune() == 'j':
		u.reminderIndex = (u.reminderIndex + 1) % len(u.reminders)
		u.renderReminder()
	case ev.Key() == tcell.KeyEnter:
		item := u.reminders[u.reminderIndex]
		u.closeOverlayQuiet()
		if item.AskID != "" {
			u.openAskPanel(item.Agent)
		} else { // a declared wait: Enter answers it like i
			u.openComposer("", item.Agent)
		}
	case ev.Key() == tcell.KeyRune && ev.Rune() == 'i':
		item := u.reminders[u.reminderIndex]
		if item.AskID == "" {
			u.closeOverlayQuiet()
			u.openComposer("", item.Agent)
		}
	}
	return nil
}
