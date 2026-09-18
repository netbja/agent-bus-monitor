package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/netbja/agent-bus-monitor/bus"
	"io"
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
		if len(args) < 3 {
			return fmt.Errorf("usage: request done <task> <reply>")
		}
		id, err := b.CompleteRequest(ctx, args[1], self, strings.Join(args[2:], " "))
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, id)
		return err
	default:
		return fmt.Errorf("request: want send|accept|block|done or [--json]")
	}
}
