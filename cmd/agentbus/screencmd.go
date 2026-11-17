package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// screenReader is all agentbus screen may ask a holder: it reads, it never
// types (agent-screen-blocked spec §2).
type screenReader interface {
	List(ctx context.Context, project string) ([]termhost.Term, error)
	Screen(ctx context.Context, id termhost.TermID, lines int) (termhost.Screen, error)
}

const (
	screenLinesDefault = 40
	screenLinesMax     = 200
)

// screenReaders may read another agent's screen from inside an agent's
// terminal; a person outside the terminals may too (the person's yes, Q2).
var screenReaders = map[string]bool{"master": true, "sentinel": true}

// runScreen is `agentbus screen <agent> [--lines N] [--json]`: the agent's
// live terminal's last screen text, plain.
//
// keyOf is the build key of a holder socket ("" unknown): inside an agent's
// terminal the holder sets its own terminal id (AGENTBUS_TERM_ID, "t1") and
// its socket (AGENTBUS_HOST), so the caller's terminal is "<key>/t1".
func runScreen(ctx context.Context, keyOf func(socket string) string, h screenReader, project string, getenv func(string) string, args []string, out, errw io.Writer) int {
	usage := func() int {
		fmt.Fprintln(errw, "usage: agentbus --project <p> screen <agent> [--lines N] [--json]")
		return 2
	}
	agent, n, asJSON := "", screenLinesDefault, false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--json":
			asJSON = true
		case a == "--lines" && i+1 < len(args):
			v, err := strconv.Atoi(args[i+1])
			if err != nil || v < 1 {
				return usage()
			}
			n, i = min(v, screenLinesMax), i+1
		case !strings.HasPrefix(a, "-") && agent == "":
			agent = a
		default:
			return usage()
		}
	}
	if agent == "" {
		return usage()
	}
	terms, err := h.List(ctx, project)
	if err != nil {
		fmt.Fprintf(errw, "agentbus screen: the holders of %s cannot be listed: %v\n", project, err)
		return 1
	}
	// The caller: inside an agent's terminal, that terminal's role; an
	// agent's shell without it, the agent it names (AGENT_BUS_AGENT).
	if self := getenv("AGENTBUS_TERM_ID"); self != "" {
		id := self
		if !strings.Contains(id, "/") {
			key := ""
			if keyOf != nil {
				key = keyOf(getenv("AGENTBUS_HOST"))
			}
			id = key + "/" + self
		}
		caller := ""
		for _, t := range terms {
			if string(t.ID) == id && t.Alive && t.Project == project {
				caller = t.Name
			}
		}
		switch {
		case caller == "":
			fmt.Fprintf(errw, "agentbus screen: this terminal %s is not a live terminal of %s; screen is master's and sentinel's\n", self, project)
			return 1
		case !screenReaders[caller]:
			fmt.Fprintf(errw, "agentbus screen: screen is master's and sentinel's; you are %s\n", caller)
			return 1
		}
	} else if name := getenv("AGENT_BUS_AGENT"); name != "" && !screenReaders[name] {
		fmt.Fprintf(errw, "agentbus screen: screen is master's and sentinel's; you are %s\n", name)
		return 1
	}
	t, ok := liveTermOf(terms, project, agent)
	if !ok {
		fmt.Fprintf(errw, "agentbus screen: no live terminal of %s in %s's holders\n", agent, project)
		return 1
	}
	sc, err := h.Screen(ctx, t.ID, n)
	if errors.Is(err, termhost.ErrScreenUntrusted) {
		fmt.Fprintf(errw, "agentbus screen: %s on %s: screen not trusted (%v)\n", agent, t.ID, err)
		return 1
	}
	if err != nil {
		fmt.Fprintf(errw, "agentbus screen: %s on %s: %v\n", agent, t.ID, err)
		return 1
	}
	lines := make([]string, 0, len(sc.Lines))
	for _, l := range sc.Lines {
		lines = append(lines, plainLine(l))
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if asJSON {
		b, _ := json.Marshal(struct {
			Agent string   `json:"agent"`
			Term  string   `json:"term"`
			Build string   `json:"build"`
			Lines []string `json:"lines"`
		}{agent, string(t.ID), t.Build, lines})
		fmt.Fprintln(out, string(b))
		return 0
	}
	fmt.Fprintf(out, "%s on %s (build %s)\n", agent, t.ID, t.Build)
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
	return 0
}

// liveTermOf is agent's live terminal: the first holder (the current build
// first, as List orders them) that has one, its newest.
func liveTermOf(terms []termhost.Term, project, agent string) (termhost.Term, bool) {
	var found termhost.Term
	ok := false
	for _, t := range terms {
		if ok && t.Build != found.Build {
			break
		}
		if t.Project == project && t.Name == agent && t.Alive {
			found, ok = t, true
		}
	}
	return found, ok
}

// plainLine drops escape sequences and control characters, so nothing the
// agent's program printed acts on the reader's terminal.
func plainLine(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		switch {
		case esc:
			if r >= '@' && r <= '~' && r != '[' {
				esc = false
			}
		case r == 0x1b:
			esc = true
		case r == '\t':
			b.WriteRune(' ')
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// runScreenCmd is screen on the project's holders: the client of every
// build's holder, seen through screenReader.
func runScreenCmd(project string, getenv func(string) string, args []string, out, errw io.Writer) int {
	h, err := holder.NewHost(project, holder.BuildID(), getenv)
	if err != nil {
		fmt.Fprintf(errw, "agentbus screen: %v\n", err)
		return 1
	}
	keyOf := func(socket string) string {
		dir, err := holder.ProjectDir(getenv, project)
		if err != nil || socket == "" {
			return ""
		}
		socks, err := holder.ProjectSockets(dir) // both names (P0-2 B)
		if err != nil {
			return ""
		}
		for key, p := range socks {
			if filepath.Clean(p) == filepath.Clean(socket) {
				return key
			}
		}
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return runScreen(ctx, keyOf, h, project, getenv, args, out, errw)
}
