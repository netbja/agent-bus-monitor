package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/quota"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/termhost"
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
	cfg = quotaListed(cfg, config.Load(config.Path(os.Getenv), "config.toml"), quota.FileProviders(quota.ConfigPath()))
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
	format := config.Machine(os.Getenv).String("display.date_format")
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
			fmt.Fprintf(tw, "%s\t%s\t%.0f%%\t%s\t%s ago\t%s\n", r.Provider, w.Name, w.UsedPct, quotaResets(w, now, format),
				roundDur(now.Sub(time.UnixMilli(r.ObservedAt))), r.Source)
		}
	}
	tw.Flush()
	return 0
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
	// display.date_format, from config.toml at each message: a change in
	// busmon's Settings applies with no restart (quota settings §2.3).
	q.DateFormat = func() string { return config.Machine(getenv).String("display.date_format") }
	return q
}

// serviceContext is the dispatch service's context pass (context-alert): each
// verified agent's fill published every minute, and the alert threshold from
// AGENT_BUS_CONTEXT_ALERT (percent, default 80, 0 = off). The desktop
// notification goes through notify-send when it is on PATH, else nowhere.
func serviceContext(getenv func(string) string, lookPath func(string) (string, error),
	run func(ctx context.Context, bin string, args ...string) error) dispatch.ContextConfig {
	settings := config.Machine(getenv)
	return dispatch.ContextConfig{Every: time.Minute, Alert: bus.ContextAlert(getenv), MaxAge: bus.ContextMaxAge,
		Urgent: settings.Float("dispatch.context_urgent"), Deadline: settings.Duration("dispatch.context_handoff_deadline"),
		Read:      func(s string) (quota.ContextReading, bool) { return quota.ReadContext(quota.ContextDir(), s) },
		Compacted: serviceCompacted(getenv, os.UserHomeDir),
		Notify:    desktopNotify(lookPath, run)}
}

// serviceCompacted reads a session's last automatic compaction (§3.5) from
// its transcript under the agent's CLAUDE_CONFIG_DIR, else the service's,
// else ~/.claude; an unreadable transcript is none (the episode ends
// silently, as it would without the notice).
func serviceCompacted(getenv func(string) string, home func() (string, error)) func(string, string, int64) (int64, bool) {
	return func(configDir, session string, afterMs int64) (int64, bool) {
		base := configDir
		if base == "" {
			base = getenv("CLAUDE_CONFIG_DIR")
		}
		if base == "" {
			h, err := home()
			if err != nil {
				return 0, false
			}
			base = filepath.Join(h, ".claude")
		}
		pre, _, found, err := tokens.AutoCompactAfter(filepath.Join(base, "projects"), session, afterMs)
		return pre, err == nil && found
	}
}

// serviceTokens is the dispatch service's tokens pass: the transcripts of
// Claude Code's projects directory ($CLAUDE_CONFIG_DIR/projects, else
// ~/.claude/projects) and the rollouts of Codex's sessions directory
// ($CODEX_HOME/sessions, else ~/.codex/sessions), {p}:tokens every 5 minutes,
// 64 files and 128 MiB per pass for both. AGENT_BUS_TOKENS=0 turns it off.
func serviceTokens(getenv func(string) string, home func() (string, error)) dispatch.TokensConfig {
	if !config.Machine(getenv).Bool("dispatch.tokens") {
		return dispatch.TokensConfig{}
	}
	h, err := home()
	if err != nil {
		h = ""
	}
	dir := func(env, under string) string { // "" when neither the env nor a home names it
		if v := getenv(env); v != "" {
			return v
		}
		if h == "" {
			return ""
		}
		return filepath.Join(h, under)
	}
	base, codex := dir("CLAUDE_CONFIG_DIR", ".claude"), dir("CODEX_HOME", ".codex")
	if base == "" && codex == "" {
		return dispatch.TokensConfig{}
	}
	c := dispatch.TokensConfig{Every: 5 * time.Minute, Budget: tokens.Budget{Files: 64, Bytes: 128 << 20}}
	if base != "" {
		c.Root = filepath.Join(base, "projects")
	}
	if codex != "" { // either one counts alone (reviewer-codex)
		c.CodexRoot = filepath.Join(codex, "sessions")
	}
	return c
}

// servicePrompt is the dispatch service's prompt notification (prompt-notify):
// AGENT_BUS_PROMPT_GRACE seconds (default 30, 0 = off), through the same
// notify-send runner as the context alert.
func servicePrompt(getenv func(string) string, lookPath func(string) (string, error),
	run func(ctx context.Context, bin string, args ...string) error) dispatch.PromptConfig {
	return dispatch.PromptConfig{Grace: dispatch.PromptGrace(getenv), Notify: desktopNotify(lookPath, run),
		FocusGrace: dispatch.FocusNotice(getenv), StuckAfter: dispatch.StuckAfter(getenv)}
}

// serviceClear is the dispatch service's auto-clear (lifecycle part B): on by
// default; AGENT_BUS_AUTO_CLEAR=0 or auto_clear = false in config.toml turns
// it off (another env value is skipped, with a warning naming the variable). An escalated clear rings the desktop through
// the context alert's runner.
func serviceClear(getenv func(string) string, lookPath func(string) (string, error),
	run func(ctx context.Context, bin string, args ...string) error) dispatch.ClearConfig {
	return dispatch.ClearConfig{On: config.Machine(getenv).Bool("dispatch.auto_clear"), Proof: 30 * time.Second, Notify: desktopNotify(lookPath, run)}
}

// servicePopRetire is the pop's end (pop-auto-retire §8): on unless
// dispatch.pop_retire is off or its delay is 0; missions lists the mission
// attempts in progress on an agent.
func servicePopRetire(getenv func(string) string, missions func(context.Context, string) ([]string, error)) dispatch.PopRetireConfig {
	r := config.Machine(getenv)
	after := r.Duration("dispatch.pop_retire_after")
	return dispatch.PopRetireConfig{On: r.Bool("dispatch.pop_retire") && after > 0, After: after, Missions: missions,
		ProcStart: dispatch.ProcStart}
}

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

// quotaThresholds reads project's quota thresholds from the files, for the
// dispatcher's refresh (quota settings §2.2): config.toml's [project.<p>]
// table, then the project's service env file (the shell at up), then the
// machine keys, then the defaults (config.ProjectValue). The process
// environment is never read: it is the service's start-time copy. A drain no
// file sets follows the warning (today's behaviour). A config.toml or env
// file that cannot be read is an error, and the dispatcher keeps the last
// values; a missing env file is none.
func quotaThresholds(getenv func(string) string, project, envFile string) func() (dispatch.QuotaThresholds, error) {
	seen := false // config.toml existed at a previous read
	return func() (dispatch.QuotaThresholds, error) {
		f := config.Load(config.Path(getenv), "config.toml")
		if !f.Exists && seen { // removed since: keep the last (spec §2.2)
			return dispatch.QuotaThresholds{}, errors.New("config.toml: missing since the last read")
		}
		seen = seen || f.Exists
		for _, w := range f.Warnings {
			if w.Key == "" { // the whole file: it cannot be read or does not parse
				return dispatch.QuotaThresholds{}, fmt.Errorf("config.toml: %s", w.Msg)
			}
		}
		ef, err := service.LoadEnvFile(envFile)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return dispatch.QuotaThresholds{}, err
		}
		get := func(name string) config.Resolved { return config.ProjectValue(f, ef, project, name) }
		th := dispatch.QuotaThresholds{Warn: get("dispatch.quota_warn").Value.(float64), Hold: get("dispatch.quota_hold").Value.(float64),
			MaxAge: get("dispatch.quota_max_age").Value.(time.Duration)}
		if d := get("dispatch.quota_drain"); d.Source == "default" {
			th.Drain = th.Warn
		} else {
			th.Drain = d.Value.(float64)
		}
		return th, nil
	}
}
