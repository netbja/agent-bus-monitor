package mergequeue

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The CI loop's pass (spec docs/superpowers/specs/2026-11-19-ci-loop-design.md §3-§5):
// for each red PR whose checks have finished, a hand-off to the head's
// author, at most ci.max_handoffs times, then a stop to the person. What is
// decided here writes nothing; every write is bus.FencedCIStep,
// bus.FencedCmdOnce (the base's directive to master) or bus.FencedCIForget.

// ciListMax is how many failing checks a text names, then "and N more".
const ciListMax = 10

// CIConfig is what a pass's loop needs beside the PRs and the ledger.
type CIConfig struct {
	Project string
	Master  string // the services' escalation target
	Cap     int    // ci.max_handoffs
	// ReadBase reads a base branch's latest commit and its rollup (one gh
	// call); called at most once per base per pass, only for a red PR that
	// reaches the base check.
	ReadBase func(ctx context.Context, branch string) (Base, error)
}

// CIDigest is an episode's dedupe key: the hex sha1 of its parts (the PR
// number or the base branch, the head, the sorted failing names), joined by
// NUL. Forge text never sits raw in the record (foureyes, r4).
func CIDigest(parts ...string) string {
	h := sha1.Sum([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])
}

func failingNames(cs []Check) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}

// CIOwner is the owner of a head (§3): the one distinct author of the ledger
// entries naming it, self-addressed verdicts ignored; master when none or
// several.
func CIOwner(vs []bus.Verdict, head, master string) string {
	authors := map[string]bool{}
	for _, v := range vs {
		if v.Head == head && v.Author != "" && v.Reviewer != v.Author {
			authors[v.Author] = true
		}
	}
	if len(authors) != 1 {
		return master
	}
	for a := range authors {
		if bus.ValidName(a) {
			return a
		}
	}
	return master
}

// ciList is "`name` (url), …", at most ciListMax, then "and N more".
func ciList(cs []Check, urls bool) string {
	var parts []string
	for i, c := range cs {
		if i == ciListMax {
			parts = append(parts, fmt.Sprintf("and %d more", len(cs)-ciListMax))
			break
		}
		s := "`" + c.Name + "`"
		if urls && c.URL != "" {
			s += " (" + c.URL + ")"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

func short(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

// ciTexts are a PR's four texts (§4, §5). The hand-off's ordinal is the
// script's; the log hint names gh run view --log-failed only when a listed
// entry is an Actions check run (A).
func ciTexts(cfg CIConfig, pr PR, owner string, failing []Check) bus.CIStep {
	where := fmt.Sprintf("PR #%d (branch `%s`, head `%s`)", pr.Number, pr.Branch, short(pr.Head))
	hint := "Read the log from the link."
	for i, c := range failing {
		if i < ciListMax && c.Actions {
			hint = "Read the log from the link or with `gh run view --log-failed`."
			break
		}
	}
	of := fmt.Sprintf(" of %d: ", cfg.Cap)
	return bus.CIStep{Owner: owner, Master: cfg.Master, Cap: cfg.Cap, PR: pr.Number,
		Handoff: [2]string{"CI failed on " + where + ", hand-off ",
			of + ciList(failing, true) + ". " + hint + " Fix it on the branch, have the fix reviewed as usual, and report to " + cfg.Master +
				fmt.Sprintf(". The loop stops after hand-off %d and gives the PR to the person.", cfg.Cap)},
		MasterCopy: [2]string{"For your information: CI failed on " + where + "; handed back to " + owner + ", hand-off ",
			of + ciList(failing, false) + "."},
		StopMaster: fmt.Sprintf("CI loop stopped: %s still fails CI after %d hand-offs (%s); the PR is the person's to decide now.",
			where, cfg.Cap, ciList(failing, false)),
		StopNotice: fmt.Sprintf("%s: PR #%d still fails CI after %d hand-offs (%s): the loop stopped; the PR is yours to decide (fix it yourself, close it, or `agentbus ci reset %d` to give the author %d more tries).",
			cfg.Project, pr.Number, cfg.Cap, ciList(failing, false), pr.Number, cfg.Cap),
	}
}

// baseVerdict is what the base says of one red PR (§4 "The base").
type baseVerdict int

const (
	baseGo     baseVerdict = iota // the loop runs as written
	baseHeld                      // shares a failing check with a red base
	baseWait                      // shares a failing check with a base still running
	baseUnread                    // the base could not be read: nothing this pass
)

func judgeBase(b Base, err error, failing []Check) baseVerdict {
	if err != nil {
		return baseUnread
	}
	for _, c := range failing {
		st, ok := b.Rollup.Status[c.Name]
		if !ok {
			continue
		}
		switch {
		case b.Rollup.Running && (st.Running || st.Failed):
			return baseWait
		case !b.Rollup.Running && st.Failed:
			return baseHeld
		}
	}
	return baseGo
}

// RunCI runs the loop over the open PRs of a pass whose gh read succeeded
// (a complete list: the closed PRs' fields are deleted). It returns the
// reason to name in the merge-queue record ("" when every write went
// through), and an error only for a lost lease. A swap in progress waits for
// the next pass, unnamed.
func RunCI(ctx context.Context, b *bus.Bus, token string, cfg CIConfig, prs []PR, vs []bus.Verdict) (string, error) {
	rec, err := b.CIRecord(ctx)
	if err != nil {
		return "ci: the record is unreadable: " + err.Error(), nil
	}
	agents, err := b.Agents(ctx)
	if err != nil {
		return "ci: agents unreadable: " + err.Error(), nil
	}
	held := func(owner string) bool {
		s := agents[owner].State
		return s == "blocked" || s == "waiting" // declared only: the turn hooks flip working and idle (§4 step 3)
	}
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

	open := map[int]bool{}
	type baseRead struct {
		b    Base
		err  error
		held int
	}
	bases := map[string]*baseRead{}
	var baseOrder []string
	for _, pr := range prs {
		open[pr.Number] = true
		if pr.Draft {
			continue
		}
		if pr.ChecksCut { // a running or failing entry may be missing: never read as finished
			reasons = append(reasons, fmt.Sprintf("ci: PR #%d has %d checks or more, more than gh lists: not read", pr.Number, ghRollupMax))
			continue
		}
		roll := Classify(pr.Checks)
		if roll.Running || len(roll.Failing) == 0 {
			continue // still running, or not red: nothing (§2)
		}
		dig := CIDigest(append([]string{strconv.Itoa(pr.Number), pr.Head}, failingNames(roll.Failing)...)...)
		r, has := rec[pr.Number]
		owner := CIOwner(vs, pr.Head, cfg.Master)
		step := ciTexts(cfg, pr, owner, roll.Failing)
		step.Digest = dig
		switch {
		case has && r.Stopped: // step 0: only its own unfinished stop
			if r.Digest != dig || strings.Contains(r.Done, "m") && strings.Contains(r.Done, "n") {
				continue
			}
		case has && r.Digest == dig: // step 1: the same episode
			parts := "om"
			if owner == cfg.Master {
				parts = "o"
			}
			complete := true
			for _, p := range parts {
				complete = complete && strings.ContainsRune(r.Done, p)
			}
			if complete || held(owner) { // a held owner delays the rest (the known limitation)
				continue
			}
		default: // a new decision
			br := bases[pr.Base]
			if br == nil {
				br = &baseRead{}
				br.b, br.err = cfg.ReadBase(ctx, pr.Base)
				bases[pr.Base] = br
				baseOrder = append(baseOrder, pr.Base)
				if br.err != nil {
					reasons = append(reasons, "ci: base "+pr.Base+" read failed: "+br.err.Error())
				}
			}
			switch judgeBase(br.b, br.err, roll.Failing) { // step 1b
			case baseHeld:
				br.held++
				continue
			case baseWait, baseUnread:
				continue
			}
			count := 0
			if has {
				count = r.Count
			}
			if count < cfg.Cap && held(owner) { // step 3: a held owner holds a hand-off, never a stop
				continue
			}
		}
		if _, err := b.FencedCIStep(ctx, token, step); note(err, fmt.Sprintf("ci: write failed for PR #%d", pr.Number)) != nil {
			return "", err
		}
	}

	// Master, once per base episode, when a red base holds a PR.
	for _, name := range baseOrder {
		br := bases[name]
		if br.err != nil || br.held == 0 || br.b.Rollup.Running || len(br.b.Rollup.Failing) == 0 {
			continue
		}
		dig := CIDigest(append([]string{name, br.b.Head}, failingNames(br.b.Rollup.Failing)...)...)
		text := fmt.Sprintf("The base `%s` is red at `%s`: %s. %d PR(s) on it fail the same checks and are held: no CI hand-off, nothing counted, until the base is green again. You own the base: fix it or have it fixed.",
			name, short(br.b.Head), ciList(br.b.Rollup.Failing, true), br.held)
		_, err := b.FencedCmdOnce(ctx, token, "ci:base:"+name, dig, "dispatch", cfg.Master, bus.CmdDirective, text, false)
		if note(err, "ci: the base's directive failed") != nil {
			return "", err
		}
	}

	var gone []int
	for n := range rec {
		if !open[n] {
			gone = append(gone, n)
		}
	}
	sort.Ints(gone)
	if note(b.FencedCIForget(ctx, token, gone), "ci: closed PRs not forgotten") != nil {
		return "", bus.ErrNotHolder
	}
	return strings.Join(reasons, "; "), nil
}
