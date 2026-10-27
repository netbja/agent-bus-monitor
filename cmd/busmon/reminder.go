package main

import (
	"sort"
	"time"
)

type attentionItem struct {
	Key      string
	Agent    string
	Text     string
	At       time.Time
	AskID    string
	Question string
	Rank     int    // the For you rank (rankAsk … rankUnread); the shell's list only
	Count    int    // an ask's open questions
	EventID  string // an unread line's entry
	By       string // a gate's challenger (the gated agent is Agent)
	Unread   bool   // an ask whose Activity line is unread: folded into its row
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
