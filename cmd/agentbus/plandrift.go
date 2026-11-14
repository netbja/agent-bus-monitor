package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/plan"
)

// planDriftMaxMerges bounds the merges a pass reads (git rev-list
// --max-count) and planDriftOutMax its output.
const (
	planDriftMaxMerges = 500
	planDriftOutMax    = 256 << 10
)

// gitRead runs a read-only git command in root, as trackedPaths does: its
// own process group, killed on the context's end, its output capped. Every
// call passes -c core.fsmonitor=false -c core.pager=cat (the architect's
// plan-drift design): a configured program never runs.
func gitRead(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = time.Second
	// The cap stops git while it writes (reviewer-codex): a writer that
	// keeps at most the cap, and past it kills the group, so an endless or
	// huge output is never buffered whole.
	out := &capWriter{max: planDriftOutMax, over: func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}}
	cmd.Stdout = out
	err := cmd.Run()
	if out.past {
		return "", fmt.Errorf("git output past %d bytes", planDriftOutMax)
	}
	if err != nil {
		return "", err
	}
	return out.buf.String(), nil
}

// capWriter keeps up to max bytes; the first write past it calls over once
// and fails, so the command's copy stops.
type capWriter struct {
	buf  strings.Builder
	max  int
	past bool
	over func()
}

func (w *capWriter) Write(p []byte) (int, error) {
	if w.past {
		return 0, errCapPast
	}
	if w.buf.Len()+len(p) > w.max {
		w.past = true
		w.over()
		return 0, errCapPast
	}
	w.buf.Write(p)
	return len(p), nil
}

var errCapPast = errors.New("output past the cap")

var gitSafe = []string{"-c", "core.fsmonitor=false", "-c", "core.pager=cat"}

// realPlanGit and realFetchAge read the repository the dispatch service runs
// in (its working directory, as tracked-secrets reads it). Nothing fetches:
// the dispatcher never touches the network for this.
func realPlanGit(ctx context.Context, args ...string) (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return gitRead(ctx, root, args...)
}

func realFetchAge(ctx context.Context) (time.Duration, error) {
	root, err := os.Getwd()
	if err != nil {
		return 0, err
	}
	return fetchAgeIn(ctx, root)
}

// fetchAgeIn is how long ago origin's main was last fetched in root's
// repository, with evidence (reviewer-codex): the newest FETCH_HEAD that
// holds a line "branch 'main' of <origin's URL>" at the very commit
// origin/main points to. A fresh FETCH_HEAD alone proves nothing: a fetch of
// another branch or remote writes one without moving origin/main. The
// files read are the common directory's and each linked worktree's (it is
// per worktree, and the agents fetch in theirs; foureyes). No evidence: an
// error, the check unknown.
func fetchAgeIn(ctx context.Context, root string) (time.Duration, error) {
	p, err := gitRead(ctx, root, append(append([]string{}, gitSafe...), "rev-parse", "--path-format=absolute", "--git-common-dir")...)
	if err != nil {
		return 0, err
	}
	common := strings.TrimSpace(p)
	tip, err := gitRead(ctx, root, append(append([]string{}, gitSafe...), "rev-parse", "--verify", "--quiet", "refs/remotes/origin/main")...)
	if err != nil {
		return 0, errors.New("no origin/main")
	}
	url, err := gitRead(ctx, root, append(append([]string{}, gitSafe...), "remote", "get-url", "origin")...)
	if err != nil {
		return 0, errors.New("no remote origin")
	}
	tip, origin := strings.TrimSpace(tip), remoteKey(strings.TrimSpace(url))
	paths := []string{filepath.Join(common, "FETCH_HEAD")}
	if more, err := filepath.Glob(filepath.Join(common, "worktrees", "*", "FETCH_HEAD")); err == nil {
		paths = append(paths, more...)
	}
	var newest time.Time
	for _, f := range paths {
		st, err := os.Lstat(f)
		if err != nil || !st.Mode().IsRegular() || st.Size() > planDriftOutMax || !st.ModTime().After(newest) {
			continue
		}
		body, err := os.ReadFile(f)
		if err == nil && fetchedMain(string(body), tip, origin) {
			newest = st.ModTime()
		}
	}
	if newest.IsZero() {
		return 0, errors.New("no fetch of origin's main at origin/main in any FETCH_HEAD")
	}
	return time.Since(newest), nil
}

// fetchedMain: a FETCH_HEAD line records origin's main (origin, as
// remoteKey) at tip.
func fetchedMain(body, tip, origin string) bool {
	for _, l := range strings.Split(body, "\n") {
		f := strings.SplitN(l, "\t", 3)
		if len(f) != 3 || f[0] != tip {
			continue
		}
		if from, ok := strings.CutPrefix(f[2], "branch 'main' of "); ok && remoteKey(from) == origin {
			return true
		}
	}
	return false
}

// remoteKey is a remote URL as FETCH_HEAD and the config may both write it:
// no scheme, no user, host and path joined by /, no trailing / or .git
// (git@github.com:o/r.git, https://github.com/o/r and github.com:o/r are
// one).
func remoteKey(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	} else if c := strings.Index(u, ":"); c >= 0 && (strings.Index(u, "/") < 0 || c < strings.Index(u, "/")) {
		u = u[:c] + "/" + u[c+1:] // scp form host:path
	}
	if at := strings.Index(u, "@"); at >= 0 && at < strings.Index(u+"/", "/") {
		u = u[at+1:]
	}
	u = strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(u, "/"), ".git"), "/")
	return strings.ToLower(u)
}

// planDrift is the check plan-drift: the merges of origin/main since the
// plan's load (first-parent, the plan's commit excluded), against each
// task's linked PR and its board branch, and the PRs master ignored. Warn,
// never fail; unknown when origin/main is not fresh or git fails.
func (s *stallReader) planDrift(ctx context.Context) (health.Outcome, error) {
	in, err := s.planDriftInput(ctx)
	if err != nil {
		return health.Outcome{Status: health.Unknown, Detail: err.Error()}, nil
	}
	s.keep(func() { s.lastDrift = &in })
	return health.EvaluatePlanDrift(in), nil
}

func (s *stallReader) planDriftInput(ctx context.Context) (health.PlanDriftInput, error) {
	var in health.PlanDriftInput
	rec, ok, err := s.b.Plan(ctx)
	if err != nil {
		return in, errors.New("the plan unavailable")
	}
	if !ok {
		return in, nil
	}
	in.Planned = true
	f, err := plan.Parse([]byte(rec.File))
	if err != nil {
		return in, errors.New("the loaded plan does not parse")
	}
	facts, err := s.b.PlanFacts(ctx)
	if err != nil {
		return in, errors.New("the plan's facts unavailable")
	}
	board, err := s.b.Board(ctx)
	if err != nil {
		return in, errors.New("the board unavailable")
	}
	if in.Ignored, err = s.b.PlanIgnored(ctx); err != nil {
		return in, errors.New("the plan's ignored PRs unavailable")
	}
	in.Links, in.Branches = map[string]int{}, map[string]string{}
	for _, t := range f.Tasks {
		if pr := facts[t.ID].PR; pr > 0 {
			in.Links[t.ID] = pr
		}
		if t.Board != "" {
			if e, on := board[t.Board]; on && e.Branch != "" {
				in.Branches[t.ID] = e.Branch
			}
		}
	}
	fetchAge, readGit := s.fetchAge, s.planGit
	if fetchAge == nil {
		fetchAge = realFetchAge
	}
	if readGit == nil {
		readGit = realPlanGit
	}
	if age, err := fetchAge(ctx); err == nil {
		in.FetchAge, in.FetchKnown = age, true
	}
	if !validCommit(rec.Commit) {
		in.GitErr = "the plan's commit is not a commit id"
		return in, nil
	}
	args := append(append([]string{}, gitSafe...), "rev-list", "--first-parent", "--format=%s",
		fmt.Sprintf("--max-count=%d", planDriftMaxMerges), rec.Commit+"..origin/main")
	out, err := readGit(ctx, args...)
	if err != nil {
		in.GitErr = "git rev-list of origin/main failed: " + err.Error()
		return in, nil
	}
	in.Merges = health.ParseMerges(out)
	return in, nil
}

// validCommit: a commit id as plan load records it (hex, 7-64): never an
// option or a range git would read otherwise.
func validCommit(c string) bool {
	if len(c) < 7 || len(c) > 64 {
		return false
	}
	for _, r := range c {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
