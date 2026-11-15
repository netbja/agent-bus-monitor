package mergequeue

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Delivered: the git test of master-idle §3.1
// (docs/superpowers/specs/2026-11-15-master-idle-design.md). A forge's
// squash-merge or rebase-merge rewrites commits, so an approved head never
// becomes an ancestor of the base; whether master's part is done is then
// git's alone, the same for every forge or none. For each head, cheapest
// first, a head matched by any step is delivered:
//
//  1. InBase: an ancestor of the base tip, or the same tree.
//  2. A squash: the zero-context patch-id of the whole branch (fork..head)
//     is one of the base's.
//  3. A rebase-merge: the patch-id of every non-merge commit of fork..head
//     is among the base's; only for a head with no merge commit in that
//     range (a merge may carry a resolution no single commit holds).
//
// The base's patch-ids are those of the non-merge commits it gained since
// the heads forked, read once per pass. Every diff is -U0 (a nearby base
// change does not alter the patch-id), --no-ext-diff --no-textconv
// --no-show-signature (no driver of the repository's configuration runs: the
// dispatcher never runs the repository's code), --no-renames (the same
// patch whatever diff.renames says). A commit's patch-id never changes, so
// Delivery keeps them by SHA between passes.

// diffFlags are every diff's flags (above).
var diffFlags = []string{"-U0", "--no-ext-diff", "--no-textconv", "--no-renames", "--no-color"}

// patchIDBatch: how many commits one git show reads.
const patchIDBatch = 256

// Delivery judges approved heads against the base; it keeps, between passes,
// each commit's patch-id and each branch's (fork, head) patch-id, dropping at
// the end of a pass what no range of that pass held. Not for concurrent
// passes (the chore runs one at a time); the lock only guards Computed.
type Delivery struct {
	// Call bounds each git command (0: the caller's context alone).
	Call time.Duration

	mu       sync.Mutex
	commit   map[string]string // commit SHA -> its patch-id ("" for no change)
	branch   map[string]string // fork + " " + head -> the branch's patch-id
	computed int               // patch-ids computed by the last pass
}

// NewDelivery is an empty cache (a restart recomputes once).
func NewDelivery() *Delivery {
	return &Delivery{commit: map[string]string{}, branch: map[string]string{}}
}

// Computed is how many patch-ids the last pass computed (the cache's test).
func (d *Delivery) Computed() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.computed
}

// size is how many patch-ids the cache holds.
func (d *Delivery) size() int { return len(d.commit) + len(d.branch) }

// Delivered tells, for each head, whether the base (its tip baseTip) holds
// its change by any of the three steps. A head this repository does not
// hold is not delivered (listed; master drops it if nobody pushes it).
func (d *Delivery) Delivered(ctx context.Context, dir string, env []string, baseTip string, heads []string) (map[string]bool, error) {
	if !bus.ValidHead(baseTip) {
		return nil, errors.New("an invalid commit id")
	}
	g := gitRun{ctx: ctx, dir: dir, env: env, call: d.Call}
	out := map[string]bool{}
	seenCommit, seenBranch := map[string]bool{}, map[string]bool{}
	computed := 0
	type pending struct{ head, fork string }
	var rest []pending
	for _, h := range heads {
		if !bus.ValidHead(h) {
			return nil, errors.New("an invalid commit id")
		}
		// rev-parse --verify --quiet exits 1, silently, only for a name it
		// cannot resolve: an absent head. A timeout or any other failure is
		// an error, the pass unknown (reviewer-codex), never "not delivered".
		here, err := g.exitTrue("rev-parse", "--verify", "--quiet", h+"^{commit}")
		if err != nil {
			return nil, err
		}
		if !here {
			out[h] = false // not here: cannot be judged, never delivered
			continue
		}
		in, err := InBase(g.bound(), dir, env, baseTip, []string{h})
		g.done()
		if err != nil {
			return nil, err
		}
		if in[h] {
			out[h] = true
			continue
		}
		fork, err := g.run(nil, "merge-base", baseTip, h)
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			out[h] = false // no common history: nothing of the base can be it
			continue
		}
		f := firstLine(fork)
		if err != nil || !bus.ValidHead(f) {
			return nil, errors.New("git merge-base: cannot judge")
		}
		rest = append(rest, pending{h, f})
	}

	// The base's patch-ids: the non-merge commits since any judged fork.
	var baseRange []string
	have := map[string]bool{}
	forks := map[string]bool{}
	for _, p := range rest {
		if forks[p.fork] {
			continue
		}
		forks[p.fork] = true
		shas, err := g.lines("rev-list", "--no-merges", p.fork+".."+baseTip)
		if err != nil {
			return nil, err
		}
		for _, s := range shas {
			if !have[s] {
				have[s] = true
				baseRange = append(baseRange, s)
			}
		}
	}
	n, err := d.commitIDs(&g, baseRange, seenCommit)
	if err != nil {
		return nil, err
	}
	computed += n
	baseIDs := map[string]bool{}
	for _, s := range baseRange {
		if id := d.commit[s]; id != "" {
			baseIDs[id] = true
		}
	}

	for _, p := range rest {
		// 2. The squash: the whole branch's patch-id.
		key := p.fork + " " + p.head
		seenBranch[key] = true
		id, ok := d.branch[key]
		if !ok {
			diff, err := g.run(nil, append(append([]string{"diff"}, diffFlags...), p.fork, p.head)...)
			if err != nil {
				return nil, errors.New("git diff: cannot judge")
			}
			ids, err := g.patchIDs(diff)
			if err != nil {
				return nil, err
			}
			for _, v := range ids {
				id = v
			}
			d.branch[key] = id
			computed++
		}
		if id != "" && baseIDs[id] {
			out[p.head] = true
			continue
		}
		// 3. The rebase-merge: every commit's, only without a merge commit.
		merges, err := g.lines("rev-list", "--min-parents=2", p.fork+".."+p.head)
		if err != nil {
			return nil, err
		}
		if len(merges) > 0 {
			out[p.head] = false
			continue
		}
		own, err := g.lines("rev-list", "--no-merges", p.fork+".."+p.head)
		if err != nil {
			return nil, err
		}
		n, err := d.commitIDs(&g, own, seenCommit)
		if err != nil {
			return nil, err
		}
		computed += n
		all, some := true, false
		for _, s := range own {
			cid := d.commit[s]
			if cid == "" {
				continue // an empty commit carries no change to find
			}
			some = true
			all = all && baseIDs[cid]
		}
		out[p.head] = some && all
	}

	// Keep only what this pass's ranges held: the map stays the ranges' size.
	for s := range d.commit {
		if !seenCommit[s] {
			delete(d.commit, s)
		}
	}
	for k := range d.branch {
		if !seenBranch[k] {
			delete(d.branch, k)
		}
	}
	d.mu.Lock()
	d.computed = computed
	d.mu.Unlock()
	return out, nil
}

// commitIDs computes the patch-ids of the SHAs not cached yet, marks every
// SHA seen, and returns how many it computed.
func (d *Delivery) commitIDs(g *gitRun, shas []string, seen map[string]bool) (int, error) {
	var missing []string
	for _, s := range shas {
		if !bus.ValidHead(s) {
			return 0, errors.New("git listed an invalid commit id")
		}
		seen[s] = true
		if _, ok := d.commit[s]; !ok {
			missing = append(missing, s)
		}
	}
	for batch := range slices.Chunk(missing, patchIDBatch) {
		args := append(append([]string{"show", "--no-show-signature", "--format=commit %H"}, diffFlags...), batch...)
		patch, err := g.run(nil, args...)
		if err != nil {
			return 0, errors.New("git show: cannot judge")
		}
		ids, err := g.patchIDs(patch)
		if err != nil {
			return 0, err
		}
		for _, s := range batch {
			d.commit[s] = ids[s] // "" when the commit changes nothing
		}
	}
	return len(missing), nil
}

// gitRun runs git in dir with env, each command bounded by call.
type gitRun struct {
	ctx    context.Context
	dir    string
	env    []string
	call   time.Duration
	cancel context.CancelFunc
}

// bound is a context for one command; done releases it.
func (g *gitRun) bound() context.Context {
	if g.call <= 0 {
		return g.ctx
	}
	ctx, cancel := context.WithTimeout(g.ctx, g.call)
	g.cancel = cancel
	return ctx
}

func (g *gitRun) done() {
	if g.cancel != nil {
		g.cancel()
		g.cancel = nil
	}
}

// run is one git command, stdin given or none.
func (g *gitRun) run(stdin []byte, args ...string) ([]byte, error) {
	ctx := g.bound()
	defer g.done()
	path, err := lookIn(g.env, "git")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = g.dir, g.env, time.Second
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("git timed out")
		}
		return nil, &runError{name: "git", err: err}
	}
	return out.Bytes(), nil
}

// exitTrue runs a command whose answer is its exit code: 0 true, 1 false,
// anything else (a timeout included) an error.
func (g *gitRun) exitTrue(args ...string) (bool, error) {
	_, err := g.run(nil, args...)
	if err == nil {
		return true, nil
	}
	var re *runError
	var ee *exec.ExitError
	if errors.As(err, &re) && errors.As(re.err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, errors.New("git " + args[0] + ": cannot judge")
}

// lines runs a listing command and returns its non-empty lines.
func (g *gitRun) lines(args ...string) ([]string, error) {
	out, err := g.run(nil, args...)
	if err != nil {
		return nil, errors.New("git " + args[0] + ": cannot judge")
	}
	return strings.Fields(string(out)), nil
}

// patchIDs: git patch-id --stable over patch, as commit (or the zero id for
// a plain diff) -> patch-id.
func (g *gitRun) patchIDs(patch []byte) (map[string]string, error) {
	ids := map[string]string{}
	if len(bytes.TrimSpace(patch)) == 0 {
		return ids, nil
	}
	out, err := g.run(patch, "patch-id", "--stable")
	if err != nil {
		return nil, errors.New("git patch-id: cannot judge")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if id, c, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			ids[c] = id
		}
	}
	return ids, nil
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return s
}
