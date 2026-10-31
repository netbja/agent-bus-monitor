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
	"strings"
	"text/tabwriter"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/quota"
	"github.com/netbja/agent-bus-monitor/tokens"
)

// runQuota prints every configured reader's reading, read now, without the
// bus: "how much is left" for the operator and for agents.
func runQuota(args []string, out, errw io.Writer) int {
	_, asJSON := extractBool(args, "--json")
	cfg, err := quota.LoadConfig(quota.ConfigPath(), exec.LookPath)
	if err != nil {
		fmt.Fprintf(errw, "quota: %v\n", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rs := quota.ReadAll(ctx, quota.Readers(cfg, time.Now))
	if asJSON {
		b, _ := json.MarshalIndent(rs, "", "  ")
		fmt.Fprintln(out, string(b))
		return 0
	}
	if len(rs) == 0 {
		fmt.Fprintln(out, "no quota reader configured (none of claude, codex, grok on PATH; see "+quota.ConfigPath()+")")
		return 0
	}
	now := time.Now()
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PROVIDER\tWINDOW\tUSED\tRESETS\tOBSERVED\tSOURCE")
	for _, r := range rs {
		switch {
		case r.Unlimited:
			fmt.Fprintf(tw, "%s\t-\tunlimited\t-\t-\t%s\n", r.Provider, r.Source)
			continue
		case r.Unknown:
			observed := "-"
			if r.AvailableAt > 0 { // no window, but the provider answered: not stopped
				observed = "served " + roundDur(now.Sub(time.UnixMilli(r.AvailableAt))) + " ago"
			}
			fmt.Fprintf(tw, "%s\t?\tunknown\t-\t%s\t%s (%s)\n", r.Provider, observed, r.Source, r.Note)
			continue
		}
		for _, w := range r.Windows {
			resets := "past"
			if d := w.Reset().Sub(now); d > 0 {
				resets = "in " + roundDur(d)
			}
			fmt.Fprintf(tw, "%s\t%s\t%.0f%%\t%s\t%s ago\t%s\n", r.Provider, w.Name, w.UsedPct, resets,
				roundDur(now.Sub(time.UnixMilli(r.ObservedAt))), r.Source)
		}
	}
	tw.Flush()
	return 0
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

// serviceQuota is the dispatch service's quota policy: the configured readers
// and the thresholds from the environment (AGENT_BUS_QUOTA_WARN, _HOLD,
// _MAX_AGE; AGENT_BUS_QUOTA=off disables it).
func serviceQuota(getenv func(string) string, log io.Writer) dispatch.QuotaConfig {
	settings := config.Machine(getenv)
	if !settings.Bool("dispatch.quota") {
		return dispatch.QuotaConfig{}
	}
	cfg, err := quota.LoadConfig(quota.ConfigPath(), exec.LookPath)
	if err != nil {
		fmt.Fprintf(log, "%s quota config: %v (built-in readers only)\n", time.Now().Format("15:04:05"), err)
	}
	readers := quota.Readers(cfg, time.Now)
	q := dispatch.QuotaConfig{Read: func(ctx context.Context) []quota.Reading { return quota.ReadAll(ctx, readers) }}
	q.WarnPct, q.HoldPct = settings.Float("dispatch.quota_warn"), settings.Float("dispatch.quota_hold")
	q.MaxAge = settings.Duration("dispatch.quota_max_age")
	if q.LongHold = settings.Duration("dispatch.quota_long_hold"); q.LongHold == 0 {
		q.LongHold = -1 // 0 = off
	}
	return q
}

// serviceContext is the dispatch service's context pass (context-alert): each
// verified agent's fill published every minute, and the alert threshold from
// AGENT_BUS_CONTEXT_ALERT (percent, default 80, 0 = off). The desktop
// notification goes through notify-send when it is on PATH, else nowhere.
func serviceContext(getenv func(string) string, lookPath func(string) (string, error),
	run func(ctx context.Context, bin string, args ...string) error) dispatch.ContextConfig {
	return dispatch.ContextConfig{Every: time.Minute, Alert: bus.ContextAlert(getenv), MaxAge: bus.ContextMaxAge,
		Read:   func(s string) (quota.ContextReading, bool) { return quota.ReadContext(quota.ContextDir(), s) },
		Notify: desktopNotify(lookPath, run)}
}

// serviceTokens is the dispatch service's tokens pass: the transcripts of
// Claude Code's projects directory ($CLAUDE_CONFIG_DIR/projects, else
// ~/.claude/projects), {p}:tokens every 5 minutes, 64 files and 128 MiB per
// pass. AGENT_BUS_TOKENS=0 turns it off.
func serviceTokens(getenv func(string) string, home func() (string, error)) dispatch.TokensConfig {
	if !config.Machine(getenv).Bool("dispatch.tokens") {
		return dispatch.TokensConfig{}
	}
	base := getenv("CLAUDE_CONFIG_DIR")
	if base == "" {
		h, err := home()
		if err != nil || h == "" {
			return dispatch.TokensConfig{}
		}
		base = filepath.Join(h, ".claude")
	}
	return dispatch.TokensConfig{Root: filepath.Join(base, "projects"), Every: 5 * time.Minute,
		Budget: tokens.Budget{Files: 64, Bytes: 128 << 20}}
}

// servicePrompt is the dispatch service's prompt notification (prompt-notify):
// AGENT_BUS_PROMPT_GRACE seconds (default 30, 0 = off), through the same
// notify-send runner as the context alert.
func servicePrompt(getenv func(string) string, lookPath func(string) (string, error),
	run func(ctx context.Context, bin string, args ...string) error) dispatch.PromptConfig {
	return dispatch.PromptConfig{Grace: dispatch.PromptGrace(getenv), Notify: desktopNotify(lookPath, run)}
}

// serviceClear is the dispatch service's auto-clear (lifecycle part B):
// AGENT_BUS_AUTO_CLEAR=1 turns it on; anything else leaves it off, the
// default until the spec's live checks pass on this machine. An escalated
// clear rings the desktop through the context alert's runner.
func serviceClear(getenv func(string) string, lookPath func(string) (string, error),
	run func(ctx context.Context, bin string, args ...string) error) dispatch.ClearConfig {
	return dispatch.ClearConfig{On: config.Machine(getenv).Bool("dispatch.auto_clear"), Proof: 30 * time.Second, Notify: desktopNotify(lookPath, run)}
}

// desktopNotify runs notify-send, bounded to 5 s; nil when it is not on PATH.
func desktopNotify(lookPath func(string) (string, error),
	run func(ctx context.Context, bin string, args ...string) error) func(ctx context.Context, title, body string) error {
	bin, err := lookPath("notify-send")
	if err != nil {
		return nil
	}
	return func(ctx context.Context, title, body string) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return run(ctx, bin, "-a", "agentbus", title, body)
	}
}

// runQuiet runs a command and discards its output.
func runQuiet(ctx context.Context, bin string, args ...string) error {
	return exec.CommandContext(ctx, bin, args...).Run()
}
