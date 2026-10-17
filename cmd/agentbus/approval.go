package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

const approvalUsage = "usage: approval ask <role> <reason...> | approval use <role> [--check | --for-swap | --by-swap <ask-id>]"

// approvalDeps: the manifest says which roles need a person's approval.
type approvalDeps struct {
	Manifest func() (*rolecfg.Manifest, error)
	Getenv   func(string) string
}

func realApprovalDeps(env []string) approvalDeps {
	return approvalDeps{
		Manifest: func() (*rolecfg.Manifest, error) {
			path, err := manifestPath(env)
			if err != nil {
				return nil, err
			}
			return rolecfg.Load(path)
		},
		Getenv: os.Getenv,
	}
}

// runApproval asks a person to approve popping a role, or spends that
// approval (v0.17 §5): agent-spawn and swap call `use`.
func runApproval(ctx context.Context, b *bus.Bus, d approvalDeps, self string, args []string, out, errw io.Writer) int {
	usage := func() int { fmt.Fprintln(errw, approvalUsage); return 2 }
	if len(args) < 2 || (args[0] != "ask" && args[0] != "use") {
		return usage()
	}
	verb, rest := args[0], args[1:]
	var mode bus.ApprovalMode = bus.SpendSpawn
	swapID := ""
	if verb == "use" {
		var check, forSwap bool
		var by string
		rest, check = extractBool(rest, "--check")
		rest, forSwap = extractBool(rest, "--for-swap")
		rest, by = extractFlag(rest, "--by-swap")
		n := 0
		for _, on := range []bool{check, forSwap, by != ""} {
			if on {
				n++
			}
		}
		if n > 1 || len(rest) != 1 {
			return usage()
		}
		switch {
		case check:
			mode = bus.CheckOnly
		case forSwap:
			mode = bus.SpendSwap
		case by != "":
			mode, swapID = bus.BySwap, by
		}
	} else if len(rest) < 2 {
		return usage()
	}
	role := rest[0]
	m, err := d.Manifest()
	if err != nil {
		fmt.Fprintf(errw, "approval: %v\n", err)
		return 2
	}
	if m.Approval(role) != "person" {
		fmt.Fprintf(errw, "approval: role %s does not need a person's approval (approval = \"person\" in roles.toml)\n", role)
		return 2
	}
	ttl := bus.DefaultApprovalTTL
	if v := d.Getenv("AGENT_BUS_APPROVAL_TTL"); v != "" {
		if ttl, err = time.ParseDuration(v); err != nil || ttl <= 0 {
			fmt.Fprintf(errw, "approval: AGENT_BUS_APPROVAL_TTL %q is not a positive duration\n", v)
			return 2
		}
	}
	if verb == "ask" {
		id, err := b.ApprovalAsk(ctx, self, role, strings.Join(rest[1:], " "))
		if err != nil {
			fmt.Fprintf(errw, "approval: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "approval ask %s for %s: answer it in busmon\n", id, role)
		return 0
	}
	id, err := b.ApprovalUse(ctx, self, role, mode, swapID, ttl)
	switch {
	case errors.Is(err, bus.ErrNoApproval):
		fmt.Fprintf(errw, "approval: %v; run `agentbus approval ask %s <reason>` and wait for a person's answer\n", err, role)
		return 3
	case err != nil:
		fmt.Fprintf(errw, "approval: %v\n", err)
		return 75
	}
	fmt.Fprintln(out, id)
	return 0
}
