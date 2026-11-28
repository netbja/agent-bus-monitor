package mergequeue

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Losable work (cleaning-up-agents §3.1 item 5): a branch a role holds open on
// the board whose local tip is not on its remote, or a stash whose message
// names the role. The merge-queue pass reads it; bus.ReadOwes takes the
// finding.

// stashListMax bounds the stash messages kept for one role.
const stashListMax = 5

// Stashes are the repository's stash messages (git stash list), newest first,
// run like LocalTips: in dir with exactly env. log.showSignature is pinned off,
// so the reflog walk under stash list starts no gpg.
func Stashes(ctx context.Context, dir string, env []string) ([]string, error) {
	out, err := run(ctx, dir, env, "git", "-c", "log.showSignature=false", "stash", "list", "--format=%gs")
	if err != nil {
		return nil, err
	}
	var msgs []string
	for _, line := range strings.Split(string(out), "\n") {
		if m := cleanText(strings.TrimSpace(line)); m != "" {
			msgs = append(msgs, m)
		}
	}
	return msgs, nil
}

// Held is a branch a role holds open on the board: a claim's branch (its
// owner) or a request's request.branch (its target).
type Held struct{ Role, Branch string }

// HeldBranches are the board's open entries' branches, by the role that holds
// each; a done entry holds nothing.
func HeldBranches(board map[string]bus.BoardEntry) []Held {
	var out []Held
	for _, e := range board {
		if !bus.BoardOwes(e.State) {
			continue
		}
		if e.Branch != "" && bus.ValidName(e.Owner) {
			out = append(out, Held{e.Owner, e.Branch})
		}
		if r := e.Request; r != nil && r.Branch != "" && bus.ValidName(r.Target) {
			out = append(out, Held{r.Target, r.Branch})
		}
	}
	return out
}

// LosableIn is one judgement's inputs.
type LosableIn struct {
	Roles   []string          // every role judged: each gets an entry, empty when it owes nothing here
	Held    []Held            // the board's open branches
	Local   map[string]string // local branch → tip
	Stashes []string          // stash messages
	// Pushed tells whether a branch's local tip is on the remote; nil when
	// there is no remote to lose it to (the local forge): nothing is
	// unpushed.
	Pushed func(branch, tip string) (bool, error)
}

// JudgeLosable is each role's losable work. A held branch with no local ref
// has nothing here to lose. A Pushed error fails the whole judgement: a
// branch not judged is never "pushed".
func JudgeLosable(in LosableIn) (map[string]bus.Losable, error) {
	out := make(map[string]bus.Losable, len(in.Roles))
	for _, role := range in.Roles {
		out[role] = bus.Losable{}
	}
	seen := map[Held]bool{}
	for _, h := range in.Held {
		l, judged := out[h.Role]
		if !judged || seen[h] {
			continue
		}
		seen[h] = true
		tip, ok := in.Local[h.Branch]
		if !ok || in.Pushed == nil {
			continue
		}
		pushed, err := in.Pushed(h.Branch, tip)
		if err != nil {
			return nil, err
		}
		if !pushed {
			l.Branches = append(l.Branches, h.Branch)
			sort.Strings(l.Branches)
			out[h.Role] = l
		}
	}
	for role, l := range out {
		re := namesRole(role)
		for _, m := range in.Stashes {
			if len(l.Stashes) < stashListMax && re.MatchString(m) {
				l.Stashes = append(l.Stashes, m)
			}
		}
		out[role] = l
	}
	return out, nil
}

// namesRole matches role as a whole name: "coder" in "On coder/x: wip", never
// in "coder2/x".
func namesRole(role string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^a-z0-9_-])` + regexp.QuoteMeta(role) + `([^a-z0-9_-]|$)`)
}
