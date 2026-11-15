package health

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"
)

// idle-master (spec docs/superpowers/specs/2026-11-15-master-idle-design.md,
// §2-§5): master idle past dispatch.master_idle_after with work only it can
// move in hand: an approved head not delivered, a request addressed to it, a
// check-back due. Pure; the reader is stallReader.idleMaster. One episode is
// the set of item keys: one wake to master, then at most one notice for the
// person, whatever its length.

// The {p}:quota marks of the guard: the wake (with its <mark>:at, the
// wake's id), the person's notice after it, and the postponed check-backs'
// notice.
const (
	StallMasterMark       = "stall-master"
	StallMasterPersonMark = "stall-master-person"
	CheckbackPersonMark   = "checkback-person"
)

// MasterHead is an approved head master has not delivered (§3.1).
type MasterHead struct{ Subject, Head string }

// MasterCheckback is master's check-back (§4); times are Unix ms, Redis time.
type MasterCheckback struct {
	What                     string
	DueMs, SetID, FirstSetID int64
	Count                    int
}

// IdleMasterInput is everything idle-master judges from.
type IdleMasterInput struct {
	Now     time.Time
	After   time.Duration // dispatch.master_idle_after; 0 = off
	ReadErr string
	Master  string      // the escalation target
	Agent   *StallAgent // nil: not registered; an answered waiting is idle since the answer
	// OpenAsks are master's questions still open (a waiting not answered).
	OpenAsks   int
	Obligation bool // a wakeable message waits for master: the dispatcher wakes it
	Paused     bool
	Held       bool
	// CannotAct is dispatch.CannotAct's reason, or "".
	CannotAct string
	// The items' sources: the merge queue's undelivered heads (QueueUnknown
	// names a failed or stale pass), master's requests, its check-back.
	Heads        []MasterHead
	QueueUnknown string
	Requests     []string
	Checkback    *MasterCheckback
	CheckbackMax int // dispatch.checkback_max; 0 = never tell the person
	// WokeEpisode and WokeAt are the stall-master mark and its :at (Unix
	// ms; 0 missing or unreadable).
	WokeEpisode string
	WokeAt      int64
}

// itemsShown is how many items a detail or a message names.
const itemsShown = 3

type masterItem struct{ key, label string }

// items are what is in master's hand: heads, requests, then the check-back
// once due, each kind in key order.
func (in IdleMasterInput) items() []masterItem {
	var heads, reqs []masterItem
	for _, h := range in.Heads {
		heads = append(heads, masterItem{"head:" + h.Head, fmt.Sprintf("approved, not pushed: %s (%s)", h.Subject, short(h.Head))})
	}
	for _, r := range in.Requests {
		reqs = append(reqs, masterItem{"req:" + r, "requested: " + r})
	}
	byKey := func(a, b masterItem) int { return strings.Compare(a.key, b.key) }
	slices.SortFunc(heads, byKey)
	slices.SortFunc(reqs, byKey)
	out := append(heads, reqs...)
	if cb := in.Checkback; cb != nil && in.Now.UnixMilli() >= cb.DueMs {
		out = append(out, masterItem{fmt.Sprintf("cb:%d", cb.SetID),
			fmt.Sprintf("check-back due %s ago: %s", minutes(in.Now.Sub(time.UnixMilli(cb.DueMs))), cb.What)})
	}
	return out
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// itemText names at most itemsShown items, then +N.
func itemText(items []masterItem) string {
	labels := make([]string, 0, itemsShown+1)
	for i, it := range items {
		if i == itemsShown {
			labels = append(labels, fmt.Sprintf("+%d", len(items)-itemsShown))
			break
		}
		labels = append(labels, it.label)
	}
	return strings.Join(labels, "; ")
}

// IdleMasterEpisode is the first 8 bytes of SHA-256 of the sorted item keys,
// in hex: the same items in any order are one episode.
func IdleMasterEpisode(in IdleMasterInput) string {
	items := in.items()
	keys := make([]string, len(items))
	for i, it := range items {
		keys[i] = it.key
	}
	slices.Sort(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	return hex.EncodeToString(sum[:8])
}

// EvaluateIdleMaster: a warn when master is idle (or done, or its waiting
// answered) for After, its pane seen not busy, no message waiting for it,
// not paused or held, with something in its hand; never a fail.
func EvaluateIdleMaster(in IdleMasterInput) Outcome {
	switch {
	case in.After <= 0:
		return Outcome{Status: Unknown, Detail: "off (master_idle_after = 0)"}
	case in.ReadErr != "":
		return Outcome{Status: Unknown, Detail: truncateDetail(in.ReadErr)}
	case in.Agent == nil:
		return Outcome{Status: OK, Detail: in.Master + " is not registered"}
	}
	a := *in.Agent
	idle := in.Now.Sub(a.DeclaredAt)
	switch {
	case a.State == "waiting":
		why := in.Master + " waits for you"
		switch {
		case in.OpenAsks == 1:
			why += " (1 ask open)"
		case in.OpenAsks > 1:
			why += fmt.Sprintf(" (%d asks open)", in.OpenAsks)
		}
		return Outcome{Status: OK, Detail: why}
	case a.State != "idle" && a.State != "done":
		return Outcome{Status: OK, Detail: in.Master + " " + a.State}
	case idle < in.After:
		return Outcome{Status: OK, Detail: in.Master + " idle " + minutes(idle)}
	case a.Pane == PaneUnseen:
		return Outcome{Status: Unknown, Detail: "cannot see " + in.Master + "'s pane (no fresh observation)"}
	case a.Pane == PaneBusy:
		return Outcome{Status: OK, Detail: in.Master + " busy (not declared)"}
	case in.Obligation:
		return Outcome{Status: OK, Detail: in.Master + " has messages; the dispatcher wakes it"}
	case in.Paused:
		return Outcome{Status: OK, Detail: "project paused"}
	case in.Held:
		return Outcome{Status: OK, Detail: in.Master + " held by its provider's limit"}
	}
	items := in.items()
	if len(items) == 0 {
		if in.QueueUnknown != "" {
			return Outcome{Status: Unknown, Detail: truncateDetail(in.QueueUnknown)}
		}
		return Outcome{Status: OK, Detail: "nothing in " + in.Master + "'s hand"}
	}
	detail := in.Master + " idle " + minutes(idle) + "; " + itemText(items)
	if in.QueueUnknown != "" {
		detail += "; " + in.QueueUnknown
	}
	if in.WokeEpisode == IdleMasterEpisode(in) && in.WokeAt <= 0 {
		detail += "; wake time unknown"
	}
	return Outcome{Status: Warn, Detail: truncateDetail(detail)}
}

// PersonNotice is one notice for the person, once per episode under Mark.
type PersonNotice struct{ Mark, Episode, Text string }

// IdleMasterAct is what one pass owes: a wake to master, or a notice for
// the person, or nothing.
type IdleMasterAct struct {
	Wake   *StallWake
	Person *PersonNotice
}

// IdleMasterActs (§5): while idle-master warns, master cannot act: the
// person's notice at once, no wake; the episode not woken yet: one wake;
// woken (its time known) at least After ago: the person's notice. The bus
// scripts keep each to once per episode.
func IdleMasterActs(in IdleMasterInput) IdleMasterAct {
	if EvaluateIdleMaster(in).Status != Warn {
		return IdleMasterAct{}
	}
	ep, what := IdleMasterEpisode(in), itemText(in.items())
	idle := minutes(in.Now.Sub(in.Agent.DeclaredAt))
	switch {
	case in.CannotAct != "":
		return IdleMasterAct{Person: &PersonNotice{StallMasterPersonMark, ep, fmt.Sprintf(
			"Stall guard: %s is idle %s with work in hand (%s) and cannot act (%s). Look at %s's pane, or tell it in busmon.",
			in.Master, idle, what, in.CannotAct, in.Master)}}
	case in.WokeEpisode != ep:
		return IdleMasterAct{Wake: &StallWake{Mark: StallMasterMark, Episode: ep, Target: in.Master, Text: fmt.Sprintf(
			"Stall guard: %s idle %s with work in hand: %s. Move it, or say why it waits: an ask, checkback with a new time, or merge-queue drop for a head that will never be pushed.",
			in.Master, idle, what)}}
	case in.WokeAt > 0 && in.Now.Sub(time.UnixMilli(in.WokeAt)) >= in.After:
		return IdleMasterAct{Person: &PersonNotice{StallMasterPersonMark, ep, fmt.Sprintf(
			"Stall guard: %s was woken %s ago for: %s. It is idle again and nothing moved. Look at %s's pane, or tell it in busmon.",
			in.Master, minutes(in.Now.Sub(time.UnixMilli(in.WokeAt))), what, in.Master)}}
	}
	return IdleMasterAct{}
}

// CheckbackPostponed (§4): master's check-back set on the same what more
// than CheckbackMax times in a row is one notice for the person, the episode
// a hash of master, the what and the count's first set id; on any pass,
// whether idle-master warns or not.
func CheckbackPostponed(in IdleMasterInput) *PersonNotice {
	cb := in.Checkback
	if cb == nil || in.CheckbackMax <= 0 || cb.Count <= in.CheckbackMax {
		return nil
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d", in.Master, strings.ToLower(strings.TrimSpace(cb.What)), cb.FirstSetID)))
	return &PersonNotice{CheckbackPersonMark, hex.EncodeToString(sum[:8]), fmt.Sprintf(
		"Stall guard: %s has postponed %q %d times since %s.", in.Master, cb.What, cb.Count, time.UnixMilli(cb.FirstSetID).Local().Format("15:04"))}
}
