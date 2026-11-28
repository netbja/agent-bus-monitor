package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/forge"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

func runRequest(ctx context.Context, b *bus.Bus, self string, args []string, out io.Writer) error {
	args, asJSON := extractBool(args, "--json")
	if len(args) == 0 {
		board, err := b.Board(ctx)
		if err != nil {
			return err
		}
		requests := make(map[string]bus.BoardEntry)
		for task, e := range board {
			if e.Request != nil {
				requests[task] = e
			}
		}
		if asJSON {
			return json.NewEncoder(out).Encode(requests)
		}
		// JSON also provides a lossless readable default; no hidden detail columns.
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(requests)
	}
	switch args[0] {
	case "priority":
		return requestPriority(ctx, b, args[1:], out)
	case "parallel": // sibling-enforce §3: the sender's or the person's exception
		return requestParallelCmd(ctx, b, self, args[1:], out)
	case "step": // sibling-enforce §3 rule 2: an abandoned step
		return requestStepCmd(ctx, b, self, args[1:], out)
	case "next":
		return requestNext(ctx, b, self, args[1:], asJSON, out)
	case "send":
		args, ref := extractFlag(args[1:], "--ref")
		args, ttlArg := extractFlag(args, "--ttl")
		var ttl time.Duration
		if ttlArg != "" {
			var err error
			ttl, err = time.ParseDuration(ttlArg)
			if err != nil {
				return err
			}
		}
		if len(args) < 3 {
			return fmt.Errorf("usage: request send <task> <target> [--ref T] [--ttl 5m] <body>")
		}
		// The person (no AGENT_BUS_AGENT, as deliver recognises them) may
		// send a task while the run drains; an agent may not (run-budget §3).
		person := requestGetenv("AGENT_BUS_AGENT") == ""
		id, err := b.RequestBy(ctx, args[0], self, args[1], ref, strings.Join(args[2:], " "), ttl, person)
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintln(out, id); err != nil {
			return err
		}
		atLimitLine(ctx, b, args[1])
		return nil
	case "accept":
		args, branch := extractFlag(args, "--branch")
		if len(args) != 2 {
			return fmt.Errorf("usage: request accept <task> [--branch <b>]")
		}
		// The project's work-in-progress limit (wip-limit §3.3): a first accept
		// at it is refused by the bus, naming what the agent holds. --branch
		// declares the work branch (code-intel §7), one owner per branch.
		return b.AcceptRequestBranch(ctx, args[1], self, wipLimit(b.Project()), branch)
	case "branch":
		if len(args) != 3 {
			return fmt.Errorf("usage: request branch <task> <branch>")
		}
		// The work branch declared later, while accepted (code-intel §7).
		return b.SetWorkBranch(ctx, args[1], self, args[2])
	case "review":
		args, branch := extractFlag(args, "--branch")
		args, commit := extractFlag(args, "--commit")
		args, more := extractBool(args, "--more")
		if commit != "" && !commitRE.MatchString(commit) {
			return fmt.Errorf("request review: --commit wants 7 to 40 lowercase hex digits, got %q", commit)
		}
		if len(args) < 2 {
			return fmt.Errorf("usage: request review <task> [--branch <b>] [--commit <sha>] [--more] [<note>]")
		}
		// A step or a PR waits on its reviewers: not in flight (wip-limit
		// §3.2); request accept resumes it. The bus checks the branch. The
		// head and the base tip read here let its merge close it
		// (board-drift §3.2); --more: a step that is not the last (§3.4).
		o := bus.ReviewOpts{Branch: branch, Note: strings.Join(args[2:], " "), More: more}
		var dir string
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
		if dir != "" {
			base, _, berr := forge.ProjectDeliveryBase(os.Getenv, b.Project())
			if berr != nil {
				base = "" // no base anybody chose: nothing recorded, nothing refused
			}
			head, rb, err := reviewFacts(reviewGitIn(dir), config.Machine(os.Getenv).String("delivery.forge"), base, branch, commit)
			if err != nil {
				return fmt.Errorf("request review: %w", err)
			}
			o.Commit, o.ReviewBase = head, rb
		}
		// One open step per request (sibling-enforce §3.1): the open steps
		// of the request are judged in this checkout; the board refuses the
		// review while one is not delivered.
		steps, err := judgeTask(ctx, b, realStepJudger(dir, b.Project()), args[1], branch, false)
		if err != nil {
			return fmt.Errorf("request review: %w", err)
		}
		o.Steps = steps
		if err := b.ReviewRequestOpts(ctx, args[1], self, o); err != nil {
			return err
		}
		auditStepsMerged(ctx, b, self, args[1], steps)
		return nil
	case "block":
		if len(args) < 3 {
			return fmt.Errorf("usage: request block <task> <reason>")
		}
		return b.BlockRequest(ctx, args[1], self, strings.Join(args[2:], " "))
	case "done":
		args, commit := extractFlag(args, "--commit")
		args, branch := extractFlag(args, "--branch")
		if commit != "" && !commitRE.MatchString(commit) {
			return fmt.Errorf("request done: --commit wants 7 to 40 lowercase hex digits, got %q", commit)
		}
		if branch != "" && !validBranch(branch) {
			return fmt.Errorf("request done: --branch %q is not a valid branch name", branch)
		}
		if len(args) < 3 {
			return fmt.Errorf("usage: request done <task> [--commit <sha>] [--branch <b>] <reply>")
		}
		steps, _, _, err := judgeDone(ctx, b, self, args[1])
		if err != nil {
			return fmt.Errorf("request done: %w", err)
		}
		id, err := b.CompleteRequestSteps(ctx, args[1], self, strings.Join(args[2:], " "), commit, branch, steps)
		if err != nil {
			return err
		}
		auditStepsMerged(ctx, b, self, args[1], steps)
		if _, err := fmt.Fprintln(out, id); err != nil {
			return err
		}
		return retroDueLine(ctx, b.RetroDue, args[1], out, os.Stderr)
	default:
		return fmt.Errorf("request: want send|accept|review|block|done|priority|parallel|step|next or [--json]")
	}
}

// retroDueLine runs the friction check after a done (self-improving loop
// §3.2): when it sends a retro it says so in one line. The done itself is
// already recorded: a failed check is told on errOut, never turned into the
// done's failure.
func retroDueLine(ctx context.Context, due func(context.Context, string) (bus.Retro, error), task string, out, errOut io.Writer) error {
	r, err := due(ctx, task)
	if err != nil {
		fmt.Fprintf(errOut, "retro check of %s failed: %v\n", task, err)
		return nil
	}
	if !r.Sent {
		return nil
	}
	_, err = fmt.Fprintf(out, "retro due: %s (%s)\n", r.Task, bus.FrictionSummary(r.Signals))
	return err
}

var commitRE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// validBranch applies git check-ref-format --branch: no whitespace or control
// characters, none of ~^:?*[\, no "..", no "@{", no leading "-", no trailing
// ".", and no empty component, component starting with "." or ending in
// ".lock" (which also rules out "//" and a trailing "/").
func validBranch(s string) bool {
	if s == "" || s == "@" || strings.HasPrefix(s, "-") || strings.HasSuffix(s, ".") ||
		strings.Contains(s, "..") || strings.Contains(s, "@{") || strings.ContainsAny(s, "~^:?*[\\ \t") {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// requestGetenv reads the caller's environment for request send (a seam for
// the tests).
var requestGetenv = os.Getenv

// requestStderr is where request send says an agent is at its limit (a seam
// for tests).
var requestStderr io.Writer = os.Stderr

// wipLimit is board.wip_limit for project: config.toml's
// [project.<project>.board], then [board], then 2 (wip-limit §3.3). No env
// variable is read.
func wipLimit(project string) int {
	f := config.Load(config.Path(requestGetenv), "config.toml")
	if n, ok := config.ProjectValue(f, nil, project, "board.wip_limit").Value.(int); ok {
		return n
	}
	return 0
}

// atLimitLine is request send's line when its target is at its limit
// (wip-limit §3.4, the person's Q5: queued, never refused): what the target
// holds, and the other agents under their limit that hold a request of this
// project, so master can route. Nothing when under the limit, no limit, or
// the board cannot be read.
func atLimitLine(ctx context.Context, b *bus.Bus, target string) {
	limit := wipLimit(b.Project())
	if limit <= 0 {
		return
	}
	board, err := b.Board(ctx)
	if err != nil {
		return
	}
	held := map[string][]string{}
	for task, e := range board {
		if r := e.Request; r != nil {
			if _, ok := held[r.Target]; !ok {
				held[r.Target] = nil
			}
			if e.State == "accepted" {
				held[r.Target] = append(held[r.Target], task)
			}
		}
	}
	mine := held[target]
	if len(mine) < limit {
		return
	}
	sort.Strings(mine)
	var others []string
	for agent, tasks := range held {
		if agent != target && len(tasks) < limit {
			others = append(others, fmt.Sprintf("%s %d/%d", agent, len(tasks), limit))
		}
	}
	sort.Strings(others)
	line := fmt.Sprintf("%s is at its wip limit (%d/%d: %s): queued", target, len(mine), limit, strings.Join(mine, ", "))
	if len(others) > 0 {
		line += "; " + strings.Join(others, ", ")
	}
	fmt.Fprintln(requestStderr, line)
}
