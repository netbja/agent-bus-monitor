package main

import (
	"context"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// A reload holds the same intent as a swap (lifecycle spec, part C); busmon
// names it for what it is.
func TestReloadingFlagNamesTheIntentsKind(t *testing.T) {
	for name, c := range map[string]struct {
		intent bus.SwapIntent
		want   string
	}{
		"swap":   {bus.SwapIntent{ToProfile: "opus"}, "swapping"},
		"reload": {bus.SwapIntent{Kind: bus.SwapKindReload}, "reloading"},
	} {
		f := &fakeReader{calls: map[string]int{}, intents: map[string]bus.SwapIntent{"coder": c.intent}}
		st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
		pollOnce(context.Background(), f, st, 1)
		var words []string
		for _, fl := range agentFlags(st.agents["coder"], false) {
			if fl.Word == "swapping" || fl.Word == "reloading" {
				words = append(words, fl.Word)
			}
		}
		if len(words) != 1 || words[0] != c.want {
			t.Errorf("%s: flags %v, want %s", name, words, c.want)
		}
	}
}
