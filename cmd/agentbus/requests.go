package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/netbja/agent-bus-monitor/bus"
	"io"
	"regexp"
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
		id, err := b.Request(ctx, args[0], self, args[1], ref, strings.Join(args[2:], " "), ttl)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, id)
		return err
	case "accept":
		if len(args) != 2 {
			return fmt.Errorf("usage: request accept <task>")
		}
		return b.AcceptRequest(ctx, args[1], self)
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
		id, err := b.CompleteRequestAt(ctx, args[1], self, strings.Join(args[2:], " "), commit, branch)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, id)
		return err
	default:
		return fmt.Errorf("request: want send|accept|block|done or [--json]")
	}
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
