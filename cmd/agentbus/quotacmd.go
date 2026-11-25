package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/internal/dispatchsvc"
	"github.com/netbja/agent-bus-monitor/quota"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// runQuota prints every configured reader's reading, read now, without the
// bus: "how much is left" for the operator and for agents.
func runQuota(project string, args []string, out, errw io.Writer) int {
	_, asJSON := extractBool(args, "--json")
	cfg, err := quota.LoadConfig(quota.ConfigPath(), exec.LookPath)
	if err != nil {
		fmt.Fprintf(errw, "quota: %v\n", err)
	}
	cfg = quotaListed(cfg, config.Load(config.Path(os.Getenv), "config.toml"), quota.FileProviders(quota.ConfigPath()))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rs := quota.ReadAll(ctx, quota.Readers(cfg, time.Now))
	if asJSON {
		b, _ := json.MarshalIndent(rs, "", "  ")
		fmt.Fprintln(out, string(b))
		return 0
	}
	th, err := dispatchsvc.QuotaThresholds(os.Getenv, project, quotaEnvFile(project))()
	if err != nil {
		fmt.Fprintf(errw, "quota thresholds: %v (the defaults shown)\n", err)
		th = dispatch.QuotaThresholds{Warn: 85, Drain: 85, Hold: 95}
	}
	if len(rs) == 0 {
		fmt.Fprintln(out, "no quota reader configured (none of claude, codex, grok on PATH; see "+quota.ConfigPath()+")")
		if len(th.Providers) == 0 {
			return 0
		}
	}
	printQuota(out, rs, time.Now(), config.Machine(os.Getenv).String("display.date_format"), th)
	return 0
}

// quotaEnvFile is project's service env file, "" without a project.
func quotaEnvFile(project string) string {
	if !bus.ValidName(project) {
		return ""
	}
	paths, err := service.PathsFrom(os.Getenv)
	if err != nil {
		return ""
	}
	return paths.EnvFile(project)
}

// printQuota is agentbus quota's table: each reading's windows, with the
// thresholds that apply to its provider and where they come from
// (quota-per-provider); a provider with thresholds but no reading last,
// inert.
func printQuota(out io.Writer, rs []quota.Reading, now time.Time, format string, th dispatch.QuotaThresholds) {
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PROVIDER\tWINDOW\tUSED\tRESETS\tOBSERVED\tSOURCE\tWARN/DRAIN/HOLD")
	for _, r := range rs {
		lv := quotaThresholdCell(th, r.Provider)
		switch {
		case r.Unlimited:
			fmt.Fprintf(tw, "%s\t-\tunlimited\t-\t-\t%s\t%s\n", r.Provider, r.Source, lv)
			continue
		case r.Unknown:
			observed := "-"
			if r.AvailableAt > 0 { // no window, but the provider answered: not stopped
				observed = "served " + roundDur(now.Sub(time.UnixMilli(r.AvailableAt))) + " ago"
			}
			fmt.Fprintf(tw, "%s\t?\tunknown\t-\t%s\t%s (%s)\t%s\n", r.Provider, observed, r.Source, r.Note, lv)
			continue
		}
		for _, w := range r.Windows {
			fmt.Fprintf(tw, "%s\t%s\t%.0f%%\t%s\t%s ago\t%s\t%s\n", r.Provider, w.Name, w.UsedPct, quotaResets(w, now, format),
				roundDur(now.Sub(time.UnixMilli(r.ObservedAt))), r.Source, lv)
		}
	}
	// A provider with thresholds of its own and no reading: stored and
	// shown, inert (no usage reader to judge it by).
	read := map[string]bool{}
	for _, r := range rs {
		read[r.Provider] = true
	}
	var inert []string
	for p := range th.Providers {
		if !read[p] {
			inert = append(inert, p)
		}
	}
	sort.Strings(inert)
	for _, p := range inert {
		fmt.Fprintf(tw, "%s\t-\tno usage reader\t-\t-\t-\t%s\n", p, quotaThresholdCell(th, p))
	}
	tw.Flush()
}

// quotaThresholdCell is provider p's thresholds in force and their source.
func quotaThresholdCell(th dispatch.QuotaThresholds, p string) string {
	lv, from := th.For(p)
	src := "default"
	switch {
	case from == dispatch.QuotaFromProvider:
		src = "quota." + p
	case from == dispatch.QuotaFromOrder && th.Providers[p] != (dispatch.QuotaLevels{}):
		src = "quota." + p + ": order broken, default"
	case from == dispatch.QuotaFromOrder:
		src = "dispatch.quota_*: order broken, default"
	}
	return fmt.Sprintf("%g/%g/%g (%s)", lv.Warn, lv.Drain, lv.Hold, src)
}

// quotaResets is a window's RESETS cell: how long, and the reset dated in
// format (display.date_format, quota settings §2.3); "past" once passed.
func quotaResets(w quota.Window, now time.Time, format string) string {
	d := w.Reset().Sub(now)
	if d <= 0 {
		return "past"
	}
	return "in " + roundDur(d) + " (" + quota.ResetLabel(w.Reset(), now, format) + ")"
}

// runStatusline is Claude Code's statusLine: it records the rate-limit
// windows and the session's context fill from stdin, then prints a status line (the --then command's, or a
// short one). It never fails the status line.
func runStatusline(args []string, stdin io.Reader, out io.Writer) int {
	args, printInstall := extractBool(args, "--print-install")
	args, then := extractFlag(args, "--then")
	if printInstall {
		return statuslineInstall(then, out)
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, 16<<20))
	if err != nil {
		return 0
	}
	if len(raw) <= quota.StatuslineMax {
		_ = quota.RecordStatusline(quota.ClaudeCachePath(), raw, time.Now())
		_ = quota.RecordContext(quota.ContextDir(), raw, time.Now()) // the session's context fill (context-alert)
	}
	if then != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "-c", then)
		cmd.Stdin = bytes.NewReader(raw)
		cmd.Stdout = out
		_ = cmd.Run()
		return 0
	}
	fmt.Fprintln(out, builtinStatusline(raw, time.Now()))
	return 0
}

// builtinStatusline is agentbus's own status line, from Claude Code's stdin
// only: model · folder · context · 5h % (reset) · 7d %. No other tool needed.
func builtinStatusline(raw []byte, now time.Time) string {
	var in struct {
		Model struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
		Workspace struct {
			CurrentDir string `json:"current_dir"`
		} `json:"workspace"`
		ContextWindow struct {
			UsedPercentage *float64 `json:"used_percentage"`
		} `json:"context_window"`
	}
	_ = json.Unmarshal(raw, &in)
	parts := []string{}
	if in.Model.DisplayName != "" {
		parts = append(parts, in.Model.DisplayName)
	}
	if d := in.Workspace.CurrentDir; d != "" {
		parts = append(parts, filepath.Base(d))
	}
	if p := in.ContextWindow.UsedPercentage; p != nil {
		parts = append(parts, fmt.Sprintf("ctx %.0f%%", *p))
	}
	if ws, err := quota.ParseStatusline(raw); err == nil {
		for _, name := range []string{"5h", "7d"} {
			for _, w := range ws {
				if w.Name != name {
					continue
				}
				part := fmt.Sprintf("%s %.0f%%", name, w.UsedPct)
				if name == "5h" && w.Reset().After(now) {
					part += " (" + w.Reset().Local().Format("15:04") + ")"
				}
				parts = append(parts, part)
			}
		}
	}
	return strings.Join(parts, " · ")
}

// statuslineInstall prints the statusLine block to put in
// ~/.claude/settings.json; it edits nothing. By default agentbus draws the
// line itself (no other tool); --then <command> chains one on purpose.
func statuslineInstall(then string, out io.Writer) int {
	exe, err := os.Executable()
	if err != nil {
		exe = "agentbus"
	}
	current, interval := currentStatusline()
	if interval <= 0 {
		interval = 10
	}
	cmd := exe + " statusline"
	if then != "" {
		cmd += " --then " + shellQuote(then)
	}
	b, _ := json.MarshalIndent(map[string]any{"statusLine": map[string]any{"type": "command", "command": cmd, "refreshInterval": interval}}, "", "  ")
	fmt.Fprintf(out, "Put this in ~/.claude/settings.json (replace the existing \"statusLine\" key):\n%s\n", b)
	if then == "" && current != "" && !strings.Contains(current, "agentbus") {
		fmt.Fprintf(out, "It replaces your current status line (%s): agentbus draws its own and needs no other tool.\n"+
			"To keep that one instead, run: agentbus statusline --print-install --then %s\n", current, shellQuote(current))
	}
	return 0
}

// currentStatusline is the statusLine command and refresh interval in
// ~/.claude/settings.json, if any.
func currentStatusline() (string, int) {
	home, _ := os.UserHomeDir()
	b, err := os.ReadFile(home + "/.claude/settings.json")
	if err != nil {
		return "", 0
	}
	var s struct {
		StatusLine struct {
			Command         string `json:"command"`
			RefreshInterval int    `json:"refreshInterval"`
		} `json:"statusLine"`
	}
	_ = json.Unmarshal(b, &s)
	return s.StatusLine.Command, s.StatusLine.RefreshInterval
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// withPopEnd lets the dispatcher end a done pop itself (pop-retire-auto,
// decision 2026-11-13-dispatcher-ends-done-pop) when it reaches the project's
// holder; without one, or with pop_retire off, master is told to run retire
// --forget as before.
func withPopEnd(c dispatch.PopRetireConfig, b *bus.Bus, rt dispatch.Runtime, getenv func(string) string) dispatch.PopRetireConfig {
	if !c.On || b == nil {
		return c
	}
	closePane, err := serviceClosePane(b.Project(), getenv, popEndCloseTimeout)
	if err != nil {
		return c
	}
	d := swapDeps{Env: getenv, ProcStart: c.ProcStart, Runtime: rt, PopRetireAfter: c.After,
		Now: time.Now, Sleep: time.Sleep, PIDGone: 15 * time.Second, Recent: 2 * time.Minute,
		ClosePane: closePane}
	c.End = func(ctx context.Context, token, role string, episode int64) dispatch.PopEnd {
		return popEnd(ctx, b, d, token, role, episode)
	}
	return c
}

// popEndCloseTimeout bounds the dispatcher's terminal close.
const popEndCloseTimeout = 10 * time.Second

// serviceClosePane closes a terminal of the project's holder (the pane an
// agent's snapshot names is its terminal's id, herdr exit §4.2), every call
// bounded by timeout and cancelled with its context (reviewer-codex: a hung
// host never holds the dispatcher's pass).
func serviceClosePane(project string, getenv func(string) string, timeout time.Duration) (func(context.Context, string) error, error) {
	h, err := holder.NewHost(project, holder.BuildID(), getenv)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, pane string) error {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := h.Close(ctx, termhost.TermID(pane)); err != nil {
			return fmt.Errorf("closing terminal %s: %w", pane, err)
		}
		return nil
	}, nil
}

// quotaListed drops the local-llm peer's built-in provider where no peer can
// run (no [llm.<name>] endpoint in config.toml): it is always unlimited, and
// listing it on every host hid "no quota reader configured". A local reader
// quota.toml names (explicit) is the person's own and always kept.
func quotaListed(cfg map[string]quota.ProviderConfig, file *config.File, explicit map[string]bool) map[string]quota.ProviderConfig {
	if _, ok := cfg["local"]; !ok || explicit["local"] || len(file.Endpoints()) > 0 {
		return cfg
	}
	out := make(map[string]quota.ProviderConfig, len(cfg))
	for k, v := range cfg {
		if k != "local" {
			out[k] = v
		}
	}
	return out
}
