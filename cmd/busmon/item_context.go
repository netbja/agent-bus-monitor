package main

import (
	"slices"
	"strings"
	"time"
)

// An item's Context (triage-dedupe spec §5, step 3): the messages attached
// to it by §2's rule (a line naming it, a waiting agent's own newest report)
// or replying on its thread (master's answer to Q2: its ref is the ask, the
// gate's challenge or the report),
// newest first, at most contextMax; a --needs report folded into the item
// first, saying so; the rest counted ("n older in Activity").

// contextMax is how many attached messages an item's page shows.
const contextMax = 3

// ctxLine is one attached message: its feed line, sender, time and headline;
// Needs and Folded when it is a --needs report folded into the item.
type ctxLine struct {
	ID     string
	Who    string
	At     time.Time
	Text   string
	Needs  string
	Folded bool
}

// itemContext is what an item's page shows of its messages.
type itemContext struct {
	Lines []ctxLine
	Older int // attached, beyond contextMax
}

// contextOf is the Context of the item keyed key among items, from the feed.
func contextOf(items []attentionItem, key string, feed []feedLine) itemContext {
	idx := slices.IndexFunc(items, func(it attentionItem) bool { return it.Key == key })
	if idx < 0 {
		return itemContext{}
	}
	it, tg, root := items[idx], liveTargets(items), itemRoot(items[idx])
	newest := map[string]string{} // agent -> its newest report's id
	for _, fl := range feed {
		if !fl.sep && fl.ev.Kind == "report" {
			newest[fl.ev.Agent] = fl.ev.ID
		}
	}
	var all []ctxLine
	for i := len(feed) - 1; i >= 0; i-- { // newest first
		fl := feed[i]
		if fl.sep || fl.ev.Kind != "report" && fl.ev.Kind != "cmd" { // an ask's own line never names its id: written before it had one
			continue
		}
		named := tg.named(lineFullText(fl))
		wait := it.Rank == rankWait && fl.ev.Kind == "report" && fl.ev.Agent == it.Agent && newest[it.Agent] == fl.ev.ID
		if !wait && !slices.Contains(named, idx) && !isReply(fl, root) {
			continue
		}
		text := fl.ev.Message
		if fl.ev.Kind == "report" || len(fl.notices) > 0 {
			text = lineHeadline(fl.ev)
		}
		text, _, _ = strings.Cut(text, "\n")
		needs := lineNeeds(fl)
		all = append(all, ctxLine{ID: fl.ev.ID, Who: sender(fl.ev), At: fl.at, Text: text, Needs: needs,
			Folded: needs != "" && (wait || len(named) == 1)})
	}
	slices.SortStableFunc(all, func(a, b ctxLine) int { // folded first, each group newest first
		switch {
		case a.Folded == b.Folded:
			return 0
		case a.Folded:
			return -1
		}
		return 1
	})
	c := itemContext{Lines: all}
	if len(all) > contextMax {
		c.Lines, c.Older = all[:contextMax], len(all)-contextMax
	}
	return c
}

// itemRoot is the thread an item stands for: an ask's id, a gate's
// challenge, a report item's report; "" for the others.
func itemRoot(it attentionItem) string {
	switch it.Rank {
	case rankAsk:
		return it.AskID
	case rankGate:
		return strings.TrimPrefix(it.Key, "gate:"+it.Agent+":")
	case rankReport:
		id, _ := lineKeyID(it.Key)
		return id
	}
	return ""
}

// isReply: the line replies on root's thread, and is not one of a report's
// own notices to the person.
func isReply(fl feedLine, root string) bool {
	if root == "" || fl.ev.Ref != root || fl.ev.ID == root {
		return false
	}
	_, _, notice := reportSubject(fl.ev.Message)
	return !notice
}
