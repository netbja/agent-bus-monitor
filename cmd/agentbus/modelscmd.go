package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/models"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// autoRefreshBudget bounds the refresh every `agentbus models` form runs on
// a table over 24 h old: 10 s in all (model-detection §3.5, foureyes). A test
// shortens it.
var autoRefreshBudget = 10 * time.Second

const modelsUsage = "usage: agentbus models [--refresh] [--provider <p>] [--json]\n" +
	"       agentbus models --changes [--since <7d|24h>] [--json]\n" +
	"       agentbus models check [--json]"

// `agentbus models` (v0.15.2 §10, model-detection §3.1, §3.4, §3.5): the
// machine's model table, one section per provider; --changes, what moved;
// check, every profile of roles.toml judged. No project, no broker.
func runModels(ctx context.Context, args []string, env models.Env, cachePath, configPath string, manifest func() (*rolecfg.Manifest, error), now time.Time, out, errw io.Writer) int {
	check := len(args) > 0 && args[0] == "check"
	if check {
		args = args[1:]
	}
	args, refresh := extractBool(args, "--refresh")
	args, asJSON := extractBool(args, "--json")
	args, changes := extractBool(args, "--changes")
	args, provider := extractFlag(args, "--provider")
	args, since := extractFlag(args, "--since")
	if len(args) != 0 || (check && (refresh || changes || provider != "" || since != "")) || (since != "" && !changes) {
		fmt.Fprintln(errw, modelsUsage)
		return 2
	}
	var window time.Duration
	if since != "" {
		d, err := parseSince(since)
		if err != nil {
			fmt.Fprintf(errw, "models: --since %s: a duration such as 7d or 24h\n", since)
			return 1
		}
		window = d
	}
	cfg, err := models.LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(errw, "models: %v\n", err)
		if refresh || check {
			return 1
		}
	}
	if refresh {
		tbl, err := models.Load(cachePath)
		if err != nil {
			fmt.Fprintf(errw, "models: %v (starting a new table)\n", err)
		}
		reportIncomplete(errw, tbl.Refresh(ctx, models.Readers(env), cfg.Tiers, now))
		if err := tbl.Save(cachePath); err != nil {
			fmt.Fprintf(errw, "models: saving %s: %v\n", cachePath, err)
			return 1
		}
	} else { // G7: any form refreshes a table over 24 h old, bounded, never blocking
		failed, held, err := refreshTable(ctx, cachePath, cfg.Tiers, models.Readers(env), now, autoRefreshBudget)
		switch {
		case err != nil:
			fmt.Fprintf(errw, "models: automatic refresh: %v\n", err)
		case held:
			fmt.Fprintln(errw, "models: refresh skipped: another refresh holds the lock")
		}
		reportIncomplete(errw, failed)
	}
	tbl, err := models.Load(cachePath)
	if err != nil {
		fmt.Fprintf(errw, "models: %v (starting a new table)\n", err)
	}
	switch {
	case check:
		return runModelsCheck(tbl, cfg, manifest, now, asJSON, out, errw)
	case changes:
		return printChanges(tbl, provider, window, now, asJSON, out, errw)
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

// reportIncomplete names each provider a refresh could not read: its rows
// are the previous ones (§3.5).
func reportIncomplete(errw io.Writer, failed map[string]string) {
	names := make([]string, 0, len(failed))
	for p := range failed {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		fmt.Fprintf(errw, "refresh incomplete: %s %s\n", p, failed[p])
	}
}

// parseSince is a Go duration, or whole days as "7d".
func parseSince(s string) (time.Duration, error) {
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("bad days")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err == nil && d <= 0 {
		err = fmt.Errorf("not positive")
	}
	return d, err
}

// printChanges is the table's events (§3.1), oldest first.
func printChanges(tbl *models.Table, provider string, window time.Duration, now time.Time, asJSON bool, out, errw io.Writer) int {
	events := []models.Event{}
	for _, e := range tbl.Events {
		if provider != "" && e.Provider != provider {
			continue
		}
		if window > 0 && now.Sub(time.UnixMilli(e.At)) > window {
			continue
		}
		events = append(events, e)
	}
	if asJSON {
		return writeJSON(out, errw, events)
	}
	if len(events) == 0 {
		fmt.Fprintln(out, "no model change recorded")
		return 0
	}
	for _, e := range events {
		what := e.ID
		switch e.Kind {
		case "default":
			what = e.ID + " (was " + dash(e.From) + ")"
		case "alias":
			what = e.ID + " now " + e.To + " (was " + dash(e.From) + ")"
		}
		fmt.Fprintf(out, "%s  %-10s %-8s %s\n", time.UnixMilli(e.At).In(now.Location()).Format("2006-01-02 15:04"), e.Provider, e.Kind, what)
	}
	return 0
}
