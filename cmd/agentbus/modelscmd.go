package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/models"
)

// `agentbus models [--refresh] [--provider p] [--json]` (v0.15.2 §10): the
// machine's model table, one section per provider. No project, no broker.
func runModels(ctx context.Context, args []string, env models.Env, cachePath, configPath string, now time.Time, out, errw io.Writer) int {
	args, refresh := extractBool(args, "--refresh")
	args, asJSON := extractBool(args, "--json")
	args, provider := extractFlag(args, "--provider")
	if len(args) != 0 {
		fmt.Fprintln(errw, "usage: agentbus models [--refresh] [--provider <p>] [--json]")
		return 2
	}
	tbl, err := models.Load(cachePath)
	if err != nil {
		fmt.Fprintf(errw, "models: %v (starting a new table)\n", err)
	}
	if refresh {
		cfg, err := models.LoadConfig(configPath)
		if err != nil {
			fmt.Fprintf(errw, "models: %v\n", err)
			return 1
		}
		tbl.Refresh(ctx, models.Readers(env), cfg.Tiers, now)
		if err := tbl.Save(cachePath); err != nil {
			fmt.Fprintf(errw, "models: saving %s: %v\n", cachePath, err)
			return 1
		}
	}
	if asJSON {
		if provider != "" {
			return writeJSON(out, errw, tbl.Providers[provider])
		}
		return writeJSON(out, errw, tbl)
	}
	names := make([]string, 0, len(tbl.Providers))
	for p := range tbl.Providers {
		if provider == "" || p == provider {
			names = append(names, p)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		fmt.Fprintln(out, "no model table yet: agentbus models --refresh")
		return 0
	}
	for _, p := range names {
		pr := tbl.Providers[p]
		state := "ok"
		if !pr.OK {
			state = "last read failed: " + pr.Error
		}
		read := "never"
		if pr.ReadAt > 0 {
			read = now.Sub(time.UnixMilli(pr.ReadAt)).Round(time.Minute).String() + " ago"
		}
		fmt.Fprintf(out, "%s  (%s; rows read %s from %s)\n", p, state, read, dash(pr.Home))
		for _, r := range pr.Rows {
			mark := " "
			if r.Default {
				mark = "*"
			}
			extra := ""
			if r.Family != "" {
				extra += " family " + r.Family
			}
			if r.Newer != "" {
				extra += " newer: " + r.Newer
			}
			fmt.Fprintf(out, "  %s %-34s %-10s %s%s\n", mark, r.ID, dash(r.Tier), r.Label, extra)
		}
	}
	return 0
}
