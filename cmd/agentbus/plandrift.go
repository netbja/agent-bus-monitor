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
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	if len(out) > planDriftOutMax {
		return "", fmt.Errorf("git output past %d bytes", planDriftOutMax)
	}
	return string(out), nil
}

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

// fetchAgeIn is how long ago FETCH_HEAD was written in root's repository
// (its common directory for a worktree): the age of origin/main.
func fetchAgeIn(ctx context.Context, root string) (time.Duration, error) {
	p, err := gitRead(ctx, root, append(append([]string{}, gitSafe...), "rev-parse", "--git-path", "FETCH_HEAD")...)
	if err != nil {
		return 0, err
	}
	p = strings.TrimSpace(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	st, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	return time.Since(st.ModTime()), nil
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
