package health

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Plan drift (the architect's design, reports 1794586011340-0 and
// 1794586021722-0; the person approved alert only): what merged on
// origin/main since the plan's load, read from local git, against the
// plan's links. A warning, never a fail, never a write to the plan.

// PlanDriftFetchMax is how old FETCH_HEAD may be before the check reads
// unknown: origin/main moves only on a fetch, and the dispatcher never
// fetches.
const PlanDriftFetchMax = time.Hour

// Merge is one merge commit of origin/main's first-parent history.
type Merge struct {
	PR     int
	Branch string
	SHA    string
}

var mergeSubject = regexp.MustCompile(`^Merge pull request #([1-9][0-9]*) from [^/\s]+/(\S+)$`)

// ParseMerges reads `git rev-list --first-parent --format=%s` output: a
// "commit <sha>" line, then the subject. Only GitHub's merge-commit form
// ("Merge pull request #N from owner/branch") is a signal; any other subject
// (a squash's "title (#N)", a local merge) is none.
func ParseMerges(out string) []Merge {
	var merges []Merge
	sha := ""
	for _, l := range strings.Split(out, "\n") {
		if s, ok := strings.CutPrefix(l, "commit "); ok {
			sha = strings.TrimSpace(s)
			continue
		}
		m := mergeSubject.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil || sha == "" {
			continue
		}
		pr, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		merges = append(merges, Merge{PR: pr, Branch: m[2], SHA: sha})
		sha = ""
	}
	return merges
}

// PlanDriftInput is one read: whether a plan is loaded, the merges since its
// load (newest first), each task's linked PR and recorded branch (the
// board's, request done --branch), the PRs master ignored (plan ignore), and
// how fresh origin/main is.
type PlanDriftInput struct {
	Planned    bool
	Merges     []Merge
	Links      map[string]int    // task -> linked PR
	Branches   map[string]string // task -> the branch its board entry records
	Ignored    map[int]bool
	FetchAge   time.Duration
	FetchKnown bool
	GitErr     string
}

var branchSuffix = regexp.MustCompile(`-[0-9]+$`)

// branchStem is a branch without a rebase's "-N" suffix (feat/x-2 -> feat/x).
func branchStem(b string) string { return branchSuffix.ReplaceAllString(b, "") }

// planDrift is the drift's lines, in a stable order; ok false when it cannot
// be judged (its reason in why).
func planDrift(in PlanDriftInput) (lines []string, why string, ok bool) {
	switch {
	case in.GitErr != "":
		return nil, in.GitErr, false
	case !in.FetchKnown:
		return nil, "origin/main's freshness unknown (no FETCH_HEAD)", false
	case in.FetchAge > PlanDriftFetchMax:
		return nil, "origin/main last fetched " + driftAge(in.FetchAge) + " ago: run git fetch", false
	}
	linked := map[int]bool{}
	tasks := make([]string, 0, len(in.Links))
	for task, pr := range in.Links {
		linked[pr] = true
		tasks = append(tasks, task)
	}
	sort.Strings(tasks)
	byPR := map[int]Merge{}
	var unlinked []string
	for _, m := range in.Merges {
		byPR[m.PR] = m
		if !linked[m.PR] && !in.Ignored[m.PR] {
			unlinked = append(unlinked, "#"+strconv.Itoa(m.PR))
		}
	}
	if len(unlinked) > 0 {
		lines = append(lines, fmt.Sprintf("%d merges not in the plan: %s (plan link <task> <pr>, or plan ignore <pr>)",
			len(unlinked), strings.Join(unlinked, " ")))
	}
	for _, task := range tasks {
		pr, branch := in.Links[task], in.Branches[task]
		if branch == "" {
			continue
		}
		if m, merged := byPR[pr]; merged {
			if m.Branch != branch {
				lines = append(lines, fmt.Sprintf("task %s linked to #%d, merged from %s, delivered on %s", task, pr, m.Branch, branch))
			}
			continue
		}
		for _, m := range in.Merges {
			if m.PR != pr && branchStem(m.Branch) == branchStem(branch) {
				lines = append(lines, fmt.Sprintf("task %s linked to #%d, but #%d merged from %s: relink", task, pr, m.PR, m.Branch))
				break
			}
		}
	}
	return lines, "", true
}

func driftAge(d time.Duration) string {
	if d >= time.Hour {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dm", int(d/time.Minute))
}

// EvaluatePlanDrift is the check plan-drift: warn on any drift, ok without,
// unknown (never ok) when origin/main is not fresh or git was not read.
func EvaluatePlanDrift(in PlanDriftInput) Outcome {
	if !in.Planned {
		return Outcome{Status: OK, Detail: "no plan loaded"}
	}
	lines, why, ok := planDrift(in)
	switch {
	case !ok:
		return Outcome{Status: Unknown, Detail: why}
	case len(lines) > 0:
		return Outcome{Status: Warn, Detail: "plan-drift: " + strings.Join(lines, "; ")}
	}
	return Outcome{Status: OK, Detail: fmt.Sprintf("%d merges since the plan's load, all linked", len(in.Merges))}
}

// PlanDriftWakes is master's wake for a drift (a fixed line, as
// quiet-agent's): once per drift, the episode being the drift itself.
func PlanDriftWakes(in PlanDriftInput, master string) []StallWake {
	if !in.Planned {
		return nil
	}
	lines, _, ok := planDrift(in)
	if !ok || len(lines) == 0 {
		return nil
	}
	text := strings.Join(lines, "; ")
	sum := sha256.Sum256([]byte(text))
	return []StallWake{{Mark: "plan-drift", Episode: hex.EncodeToString(sum[:8]), Target: master,
		Text: "Plan drift: " + text + ". Link each merge to its task (agentbus plan link <task> <pr>) or ignore it (agentbus plan ignore <pr>)."}}
}
