package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/memory"
)

const memUsage = `usage: agentbus mem check [--stale-after 60d] [--strict] | mem new <lesson|decision|question> <slug> --title "<title>" [--scope transferable] [--note-project P] | mem sync   (all: [--root DIR])`

// runMem is `agentbus mem`: the shared-memory tooling. It reads and writes
// files under the repository root only — no project, no broker — so main runs
// it before the project check. Exit 0 ok, 1 check failed or refused, 2 usage.
func runMem(args []string, getenv func(string) string, cwd string, today time.Time, stdout, stderr io.Writer) int {
	usage := func() int { fmt.Fprintln(stderr, memUsage); return 2 }
	fail := func(err error) int { fmt.Fprintln(stderr, "error: "+err.Error()); return 1 }
	if len(args) == 0 {
		return usage()
	}
	sub, rest := args[0], args[1:]
	rest, root := extractFlag(rest, "--root")

	var (
		staleAfter time.Duration
		strict     bool
		title      string
		newOpts    memory.NewOptions
	)
	switch sub {
	case "check":
		var stale string
		rest, stale = extractFlag(rest, "--stale-after")
		rest, strict = extractBool(rest, "--strict")
		if stale != "" {
			n, err := strconv.Atoi(strings.TrimSuffix(stale, "d"))
			if !strings.HasSuffix(stale, "d") || err != nil || n < 1 {
				return usage()
			}
			staleAfter = time.Duration(n) * 24 * time.Hour
		}
		if len(rest) != 0 {
			return usage()
		}
	case "new":
		rest, title = extractFlag(rest, "--title")
		rest, newOpts.Scope = extractFlag(rest, "--scope")
		rest, newOpts.NoteProject = extractFlag(rest, "--note-project")
		if len(rest) != 2 || title == "" || strings.HasPrefix(rest[0], "--") || strings.HasPrefix(rest[1], "--") {
			return usage()
		}
		newOpts.Kind, newOpts.Slug, newOpts.Title = rest[0], rest[1], title
	case "sync":
		if len(rest) != 0 {
			return usage()
		}
	default:
		return usage()
	}

	if root == "" {
		r, err := memory.FindRoot(cwd)
		if err != nil {
			return fail(err)
		}
		root = r
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "memory", "INDEX.md")); err != nil {
		return fail(fmt.Errorf("no docs/memory/INDEX.md under %s", root))
	}

	switch sub {
	case "check":
		r := memory.Check(memory.Options{Root: root, Today: today, StaleAfter: staleAfter})
		for _, s := range r.Optional {
			fmt.Fprintln(stdout, "OPTIONAL: "+s)
		}
		for _, s := range r.Warnings {
			fmt.Fprintln(stdout, "WARN: "+s)
		}
		for _, s := range r.Errors {
			fmt.Fprintln(stdout, "FAIL: "+s)
		}
		if len(r.Errors) > 0 || (strict && len(r.Warnings) > 0) {
			return 1
		}
		fmt.Fprintf(stdout, "PASS: memory entry points, boot references, local links, metadata, reachability and index block; %d optional missing file(s), %d warning(s)\n",
			len(r.Optional), len(r.Warnings))
	case "new":
		newOpts.Root, newOpts.Author, newOpts.Today = root, getenv("AGENT_BUS_AGENT"), today
		rel, err := memory.NewNote(newOpts)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, rel)
	case "sync":
		changed, err := memory.Sync(root)
		if err != nil {
			return fail(err)
		}
		if changed {
			fmt.Fprintln(stdout, "AGENTS.md: index block updated")
		} else {
			fmt.Fprintln(stdout, "AGENTS.md: index block already up to date")
		}
	}
	return 0
}
