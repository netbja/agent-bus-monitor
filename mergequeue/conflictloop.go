package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The conflict hand-off (spec
// docs/superpowers/specs/2026-11-21-conflict-owner-design.md §4): an
// approved head git finds conflicting goes back to its owner (§3), once per
// episode (the PR, the head), counted up to conflict.max_handoffs; past the
// cap or conflict.escalate_after, master has it. {p}:conflicts holds the
// count (bus.FencedConflictTrack); every directive is a bus.FencedCmdOnce.

// ConflictConfig is the loop's settings for one pass.
type ConflictConfig struct {
	Master        string
	Cap           int           // conflict.max_handoffs
	EscalateAfter time.Duration // conflict.escalate_after; 0: no time bound
}

// ConflictPass is what one pass of the merge queue saw. Open must be the
// complete set of the open PRs' fields (gh) or the live branches' (git): a
// field not in it is forgotten.
type ConflictPass struct {
	Base        string
	Local       bool                // local mode: the base is the local branch, there is no origin (reviewer-codex)
	Now         time.Time           // Redis time
	Conflicting []bus.MergeConflict // the pass's; git confirmed those with a BaseTip
	Clean       []string            // the fields of the approved heads git judged clean this pass
	Open        map[string]bool     // the fields of the PRs (or branches) still open
}

// ConflictField is a PR's field ("pr:<n>"), or a branch's in the git
// fallback ("br:<digest of its name>").
func ConflictField(number int, branch string) string {
	if number > 0 {
		return "pr:" + strconv.Itoa(number)
	}
	return "br:" + CIDigest("branch", branch)
}

// ConflictDigest is a conflict's episode: (the PR or branch, the head); the
// base tip is not part of it (the person, r5).
func ConflictDigest(c bus.MergeConflict) string {
	who := c.Branch
	if c.Number > 0 {
		who = strconv.Itoa(c.Number)
	}
	return CIDigest("conflict", who, c.Head)
}

// conflictReserve is one candidate's writes (a track, up to two sends): a
// candidate is not started with less of the pass's budget left.
const conflictReserve = time.Second

// conflictFilesMax is how many files a hand-off names.
const conflictFilesMax = 10

func conflictWhere(c bus.MergeConflict) string {
	if c.Number > 0 {
		return fmt.Sprintf("PR #%d (%s)", c.Number, c.Branch)
	}
	return "Branch " + c.Branch
}

func conflictHandoff(c bus.MergeConflict, base string, local bool, k, cap int) string {
	onto := "origin/" + base
	if local {
		onto = base
	}
	files := c.Files
	more := ""
	if len(files) > conflictFilesMax {
		more = fmt.Sprintf(" and %d more", len(files)-conflictFilesMax)
		files = files[:conflictFilesMax]
	}
	return fmt.Sprintf("%s at %s conflicts with %s at %s: %s%s. Rebase it onto %s, resolve, test, and have the new head reviewed; it is delivered as usual. Hand-off %d of %d.",
		conflictWhere(c), short(c.Head), base, short(c.BaseTip), strings.Join(files, ", "), more, onto, k, cap)
}

// conflictAge is an age in hours and minutes ("3h0m", "45m").
func conflictAge(d time.Duration) string {
	d = max(0, d).Truncate(time.Minute)
	return strings.TrimSuffix(d.String(), "0s")
}

// RunConflicts runs the hand-off over one pass (§4 "The pass"). It returns
// what failed, "" when every write went through, and an error only for a
// lost lease. A swap in progress waits for the next pass, unnamed.
func RunConflicts(ctx context.Context, b *bus.Bus, token string, cfg ConflictConfig, p ConflictPass) (string, error) {
	var reasons []string
	note := func(err error, what string) error {
		switch {
		case err == nil, errors.Is(err, bus.ErrSwapInProgress):
			return nil
		case errors.Is(err, bus.ErrNotHolder):
			return err
		}
		reasons = append(reasons, what+": "+err.Error())
		return nil
	}
	done := func() string { return strings.Join(reasons, "; ") }
	rec, err := b.ConflictRecord(ctx)
	if err != nil {
		return "conflicts: the record is unreadable: " + err.Error(), nil
	}
	snaps, err := b.Agents(ctx)
	if err != nil {
		return "conflicts: agents unreadable: " + err.Error(), nil
	}
	board, err := b.Board(ctx)
	if err != nil {
		return "conflicts: the board is unreadable: " + err.Error(), nil
	}
	known := map[string]bool{}
	for n := range snaps {
		known[n] = true
	}
	held := func(owner string) bool {
		s := snaps[owner].State
		return s == "blocked" || s == "waiting" // declared only, as the CI loop's
	}

	// Step 1, resolved: a clean approved head, or a PR (branch) no longer open.
	var gone []string
	for f := range rec {
		if !p.Open[f] || slices.Contains(p.Clean, f) {
			gone = append(gone, f)
		}
	}
	slices.Sort(gone)
	if err := note(b.FencedConflictForget(ctx, token, gone), "conflicts: forget"); err != nil {
		return "", err
	}

	var cands []bus.MergeConflict
	for _, c := range p.Conflicting {
		if c.BaseTip != "" { // confirmed by git (§2); the forge's flag alone is not handed off
			cands = append(cands, c)
		}
	}
	for i, c := range cands {
		if dl, ok := ctx.Deadline(); ctx.Err() != nil || ok && time.Until(dl) < conflictReserve {
			reasons = append(reasons, fmt.Sprintf("conflicts: %d left for the next pass", len(cands)-i))
			break
		}
		field, digest := ConflictField(c.Number, c.Branch), ConflictDigest(c)
		marks := bus.ConflictMarks(field)
		owner := ConflictOwner(board, known, c.Branch, cfg.Master)
		what := "conflicts: " + conflictWhere(c)
		escalation := func(e bus.ConflictEntry) error { // the escalation's own send: once, by its mark
			age := p.Now.Sub(time.Unix(e.First, 0))
			text := fmt.Sprintf("%s still conflicts with %s after %d hand-off(s) / %s; owner %s. Take it over or reassign it.",
				conflictWhere(c), p.Base, e.Count, conflictAge(age), owner)
			_, err := b.FencedCmdOnce(ctx, token, marks[2], strconv.FormatInt(e.First, 10), "dispatch", cfg.Master, "directive", text, false)
			return note(err, what)
		}
		escalate := func() error {
			e, err := b.FencedConflictTrack(ctx, token, bus.ConflictTrack{Field: field, Digest: digest, Escalate: true})
			if err != nil {
				return note(err, what)
			}
			return escalation(e)
		}
		handoff := func(k int) error {
			text := conflictHandoff(c, p.Base, p.Local, k, cfg.Cap)
			if _, err := b.FencedCmdOnce(ctx, token, marks[0], digest, "dispatch", owner, "directive", text, false); err != nil {
				return note(err, what) // the copy waits for the next pass too
			}
			if owner == cfg.Master {
				return nil
			}
			_, err := b.FencedCmdOnce(ctx, token, marks[1], digest, "dispatch", cfg.Master, "directive",
				"For your information, handed back to "+owner+": "+text, true)
			return note(err, what)
		}

		e, has := rec[field]
		var err error
		switch {
		case has && e.Escalated: // step 2: only the escalation's own send, if it is missing
			err = escalation(e)
		case has && cfg.EscalateAfter > 0 && p.Now.Sub(time.Unix(e.First, 0)) >= cfg.EscalateAfter: // step 3
			err = escalate()
		case has && e.Last == digest: // step 5: the same episode; only what a crash left unsent
			err = handoff(e.Count)
		case held(owner): // step 4, a held owner: first set, nothing counted or sent
			_, terr := b.FencedConflictTrack(ctx, token, bus.ConflictTrack{Field: field, Digest: digest})
			err = note(terr, what)
		case e.Count < cfg.Cap: // step 4: a new episode under the cap
			ne, terr := b.FencedConflictTrack(ctx, token, bus.ConflictTrack{Field: field, Digest: digest, Count: true})
			if terr != nil {
				err = note(terr, what)
				break
			}
			err = handoff(ne.Count)
		default: // step 4, the cap reached (a cap of 0: the first)
			err = escalate()
		}
		if err != nil {
			return "", err
		}
	}
	return done(), nil
}
