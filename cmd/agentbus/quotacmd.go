package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/netbja/agent-bus-monitor/quota"
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
			fmt.Fprintf(tw, "%s\t?\tunknown\t-\t-\t%s (%s)\n", r.Provider, r.Source, r.Note)
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
// windows from stdin, then prints a status line (the --then command's, or a
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
	fmt.Fprintln(out, builtinStatusline(raw))
	return 0
}

// builtinStatusline: model · 5h N% · 7d N%.
func builtinStatusline(raw []byte) string {
	var in struct {
		Model struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	}
	_ = json.Unmarshal(raw, &in)
	parts := []string{}
	if in.Model.DisplayName != "" {
		parts = append(parts, in.Model.DisplayName)
	}
	if ws, err := quota.ParseStatusline(raw); err == nil {
		for _, name := range []string{"5h", "7d"} {
			for _, w := range ws {
				if w.Name == name {
					parts = append(parts, fmt.Sprintf("%s %.0f%%", name, w.UsedPct))
				}
			}
		}
	}
	return strings.Join(parts, " · ")
}

// statuslineInstall prints the statusLine block to put in
// ~/.claude/settings.json; it edits nothing.
func statuslineInstall(then string, out io.Writer) int {
	exe, err := os.Executable()
	if err != nil {
		exe = "agentbus"
	}
	current, interval := currentStatusline()
	if then == "" {
		then = current
	}
	if interval <= 0 {
		interval = 60
	}
	cmd := exe + " statusline"
	if then != "" && !strings.Contains(then, "agentbus") {
		cmd += " --then " + shellQuote(then)
	}
	b, _ := json.MarshalIndent(map[string]any{"statusLine": map[string]any{"type": "command", "command": cmd, "refreshInterval": interval}}, "", "  ")
	fmt.Fprintf(out, "Put this in ~/.claude/settings.json (replace the existing \"statusLine\" key):\n%s\n", b)
	if then != "" && !strings.Contains(then, "agentbus") {
		fmt.Fprintln(out, "Your current status line is kept through --then; drop --then to use agentbus's own line.")
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
