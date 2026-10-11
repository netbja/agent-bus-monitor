package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/memory"
	"github.com/netbja/agent-bus-monitor/mission"
)

const (
	missionsUsage = "usage: missions [--repo DIR] [--escalate-to AGENT] | missions --status"
	missionUsage  = "usage: mission load <path>@<commit> [--wait 30s] | mission retry <change>/<id> | mission abort <change> --reason <text> | mission status [<change>] | mission show <change>/<id>"
)

// runMissions is `agentbus missions`: the conductor process, in the
// foreground (a herdr tab, like dispatch).
func runMissions(ctx context.Context, b *bus.Bus, args []string, cwd string, stdout, stderr io.Writer) int {
	args, status := extractBool(args, "--status")
	args, repo := extractFlag(args, "--repo")
	args, escalate := extractFlag(args, "--escalate-to")
	if len(args) != 0 {
		fmt.Fprintln(stderr, missionsUsage)
		return 2
	}
	if status {
		if alive, err := b.ConductorAlive(ctx); err != nil {
			fmt.Fprintln(stderr, "error: "+err.Error())
			return 1
		} else if alive {
			fmt.Fprintln(stdout, "missions up")
			return 0
		}
		fmt.Fprintln(stdout, "missions down")
		return 1
	}
	cfg := mission.DefaultRunConfig()
	cfg.Log = func(format string, a ...any) { fmt.Fprintf(stderr, time.Now().Format("15:04:05 ")+format+"\n", a...) }
	if escalate != "" {
		if !bus.ValidName(escalate) {
			fmt.Fprintln(stderr, missionsUsage)
			return 2
		}
		cfg.EscalateTo = escalate
	}
	if repo == "" {
		r, err := memory.FindRoot(cwd)
		if err != nil {
			fmt.Fprintln(stderr, "error: "+err.Error()+" (pass --repo)")
			return 1
		}
		repo = r
	}
	cfg.Repo = repo
	switch err := mission.Run(ctx, b, cfg); {
	case errors.Is(err, mission.ErrAlreadyRunning):
		fmt.Fprintln(stdout, "conductor already running")
		return 0
	case err != nil:
		fmt.Fprintln(stderr, "error: "+err.Error())
		return 1
	}
	return 0
}

// runMission is `agentbus mission …`: reads go to Redis directly; changes are
// queued for the conductor, which alone applies them.
func runMission(ctx context.Context, b *bus.Bus, self string, args []string, stdout, stderr io.Writer) int {
	usage := func() int { fmt.Fprintln(stderr, missionUsage); return 2 }
	if len(args) == 0 {
		return usage()
	}
	sub, rest := args[0], args[1:]
	rest, waitArg := extractFlag(rest, "--wait")
	wait := 30 * time.Second
	if waitArg != "" {
		d, err := time.ParseDuration(waitArg)
		if err != nil || d <= 0 {
			return usage()
		}
		wait = d
	}
	var opArgs []string
	switch sub {
	case "status":
		if len(rest) > 1 {
			return usage()
		}
		return missionStatus(ctx, b, rest, stdout, stderr)
	case "show":
		if len(rest) != 1 || !strings.Contains(rest[0], "/") {
			return usage()
		}
		return missionShow(ctx, b, rest[0], stdout, stderr)
	case "load":
		if len(rest) != 1 || !strings.Contains(rest[0], "@") {
			return usage()
		}
		opArgs = rest
	case "retry":
		if len(rest) != 1 || !strings.Contains(rest[0], "/") {
			return usage()
		}
		opArgs = rest
	case "abort":
		var reason string
		rest, reason = extractFlag(rest, "--reason")
		if len(rest) != 1 || strings.TrimSpace(reason) == "" {
			return usage()
		}
		opArgs = []string{rest[0], reason}
	default:
		return usage()
	}
	if alive, err := b.ConductorAlive(ctx); err != nil {
		fmt.Fprintln(stderr, "error: "+err.Error())
		return 1
	} else if !alive {
		fmt.Fprintln(stderr, "missions down: start agentbus missions")
		return 1
	}
	id, err := b.SubmitMissionOp(ctx, self, sub, opArgs)
	if err != nil {
		fmt.Fprintln(stderr, "error: "+err.Error())
		return 1
	}
	ok, msg, found, err := b.WaitMissionOpResult(ctx, id, wait)
	switch {
	case err != nil:
		fmt.Fprintln(stderr, "error: "+err.Error())
		return 1
	case !found:
		fmt.Fprintf(stderr, "queued as %s, not applied yet\n", id)
		return 1
	case !ok:
		fmt.Fprintln(stderr, "error: "+msg)
		return 1
	}
	fmt.Fprintln(stdout, msg)
	return 0
}

func missionStatus(ctx context.Context, b *bus.Bus, rest []string, stdout, stderr io.Writer) int {
	s := mission.Store{B: b}
	names := rest
	if len(names) == 0 {
		var err error
		if names, err = s.Changes(ctx); err != nil {
			fmt.Fprintln(stderr, "error: "+err.Error())
			return 1
		}
	}
	if len(names) == 0 {
		fmt.Fprintln(stdout, "no change loaded")
		return 0
	}
	for _, name := range names {
		ch, ok, err := s.Change(ctx, name)
		if err != nil || !ok {
			fmt.Fprintf(stderr, "error: unknown change %s\n", name)
			return 1
		}
		ms, err := s.Missions(ctx, name)
		if err != nil {
			fmt.Fprintln(stderr, "error: "+err.Error())
			return 1
		}
		fmt.Fprintf(stdout, "%s rev %d (%s) %s\n", ch.Change, ch.Rev, ch.State, ch.Branch)
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  ID\tSTATE\tATTEMPT\tAGENT\tREVIEWER\tREASON")
		ids := make([]string, 0, len(ms))
		for id := range ms {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			r := ms[id]
			reason, _, _ := strings.Cut(r.Reason, "\n")
			if len(reason) > 70 {
				reason = reason[:67] + "..."
			}
			fmt.Fprintf(tw, "  %s\t%s\t%d\t%s\t%s\t%s\n", r.ID, r.State, r.Attempt, r.Current.Agent, r.Reviewer, reason)
		}
		tw.Flush()
	}
	return 0
}

func missionShow(ctx context.Context, b *bus.Bus, ref string, stdout, stderr io.Writer) int {
	name, id, _ := strings.Cut(ref, "/")
	ms, err := mission.Store{B: b}.Missions(ctx, name)
	if err != nil {
		fmt.Fprintln(stderr, "error: "+err.Error())
		return 1
	}
	r, ok := ms[id]
	if !ok {
		fmt.Fprintf(stderr, "error: unknown mission %s\n", ref)
		return 1
	}
	out, _ := json.MarshalIndent(r, "", "  ")
	fmt.Fprintln(stdout, string(out))
	return 0
}
