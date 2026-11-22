package main

import (
	"slices"
	"strings"
	"time"
)

// An item's Context (triage-dedupe spec §5, step 3): the messages attached
// to it by §2's rule (a line naming it, a waiting agent's own newest report),
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
	it, tg := items[idx], liveTargets(items)
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
		if !wait && !slices.Contains(named, idx) {
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
