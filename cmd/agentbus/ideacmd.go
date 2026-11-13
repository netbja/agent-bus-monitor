package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

const ideaUsage = "usage: idea add <title...> [--text <text>] [--source <where>] [--as <person>] | idea list [--state <state>] [--json] | idea set <id> <state> [--from <state>] [--as <person>]"

// runIdea is the project's ideas (bus/ideas.go): add is open to agents and
// always starts at "to discuss"; set is the person's, never an agent's.
func runIdea(ctx context.Context, b *bus.Bus, args []string, getenv func(string) string, out, errw io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errw, ideaUsage)
		return 2
	}
	switch args[0] {
	case "add":
		return ideaAdd(ctx, b, args[1:], getenv, out, errw)
	case "list":
		return ideaList(ctx, b, args[1:], out, errw)
	case "set":
		return ideaSet(ctx, b, args[1:], getenv, out, errw)
	}
	fmt.Fprintln(errw, ideaUsage)
	return 2
}

// ideaPerson is the person's name: --as, else identity.as (its variable,
// then the files). Never "pilot".
func ideaPerson(as string, getenv func(string) string) string {
	if as == "" {
		as = getenv(config.EnvName("identity.as"))
	}
	if as == "" {
		as = settingsIdentity(getenv)
	}
	return strings.ToLower(strings.TrimSpace(as))
}

func ideaAdd(ctx context.Context, b *bus.Bus, args []string, getenv func(string) string, out, errw io.Writer) int {
	args, text := extractFlag(args, "--text")
	args, source := extractFlag(args, "--source")
	args, as := extractFlag(args, "--as")
	title := strings.TrimSpace(strings.Join(args, " "))
	if title == "" {
		fmt.Fprintln(errw, ideaUsage)
		return 2
	}
	by := getenv("AGENT_BUS_AGENT")
	switch {
	case by != "" && as != "":
		fmt.Fprintf(errw, "idea add: %s is an agent: it adds under its own name, never --as a person\n", by)
		return 1
	case by == "":
		by = ideaPerson(as, getenv)
		if by == "" {
			fmt.Fprintln(errw, "idea add: name yourself: --as <person> (or record it once: agentbus identity set <name>)")
			return 2
		}
	}
	id, err := b.AddIdea(ctx, bus.IdeaDef{Title: title, Text: text, Source: source, By: by})
	if err != nil {
		fmt.Fprintf(errw, "idea add: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "%s added: %s (%s)\n", id, title, bus.IdeaToDiscuss)
	return 0
}

func ideaList(ctx context.Context, b *bus.Bus, args []string, out, errw io.Writer) int {
	args, asJSON := extractBool(args, "--json")
	args, state := extractFlag(args, "--state")
	if len(args) != 0 {
		fmt.Fprintln(errw, ideaUsage)
		return 2
	}
	if state != "" {
		s, ok := bus.ParseIdeaState(state)
		if !ok {
			fmt.Fprintf(errw, "idea list: a state is one of %s\n", strings.Join(bus.IdeaStates, ", "))
			return 2
		}
		state = s
	}
	all, err := b.Ideas(ctx)
	if err != nil {
		fmt.Fprintf(errw, "idea list: %v\n", err)
		return 1
	}
	ideas := []bus.Idea{}
	for _, it := range all {
		if state == "" || it.State == state {
			ideas = append(ideas, it)
		}
	}
	if asJSON {
		raw, _ := json.MarshalIndent(ideas, "", "  ")
		fmt.Fprintln(out, string(raw))
		return 0
	}
	if len(ideas) == 0 {
		fmt.Fprintln(out, "no idea")
		return 0
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, it := range ideas {
		set := "added by " + it.By
		if by, at := it.SetBy(); by != "" {
			set = fmt.Sprintf("set by %s %s ago", by, roundDur(time.Since(time.UnixMilli(at))))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", it.ID, it.State, it.Title, set)
	}
	tw.Flush()
	return 0
}

func ideaSet(ctx context.Context, b *bus.Bus, args []string, getenv func(string) string, out, errw io.Writer) int {
	args, from := extractFlag(args, "--from")
	args, as := extractFlag(args, "--as")
	if len(args) != 2 {
		fmt.Fprintln(errw, ideaUsage)
		return 2
	}
	to, ok := bus.ParseIdeaState(args[1])
	if !ok {
		fmt.Fprintf(errw, "idea set: a state is one of %s\n", strings.Join(bus.IdeaStates, ", "))
		return 2
	}
	// The person's own shell only: an agent's environment or a herdr pane
	// is refused. A guard against accidents, not a proof (same uid).
	if a, pane := getenv("AGENT_BUS_AGENT"), getenv("HERDR_PANE_ID"); a != "" || pane != "" {
		fmt.Fprintln(errw, "idea set: only the person sets an idea's state, from their own shell or busmon (not from an agent's environment or a herdr pane)")
		return 1
	}
	by := ideaPerson(as, getenv)
	if by == "" || by == "pilot" {
		fmt.Fprintln(errw, "idea set: name yourself: --as <person> (or record it once: agentbus identity set <name>)")
		return 2
	}
	id := args[0]
	if from == "" { // the state as read now: the compare-and-set still refuses a change in between
		ideas, err := b.Ideas(ctx)
		if err != nil {
			fmt.Fprintf(errw, "idea set: %v\n", err)
			return 1
		}
		for _, it := range ideas {
			if it.ID == id {
				from = it.State
			}
		}
		if from == "" {
			fmt.Fprintf(errw, "idea set: no idea %s\n", id)
			return 1
		}
	}
	if err := b.SetIdeaState(ctx, id, from, to, by); err != nil {
		fmt.Fprintf(errw, "idea set: %v\n", err)
		return 1
	}
	f, _ := bus.ParseIdeaState(from)
	fmt.Fprintf(out, "%s: %s → %s (by %s)\n", id, f, to, by)
	return 0
}
