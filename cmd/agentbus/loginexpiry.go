package main

import (
	"path/filepath"
	"runtime"
	"time"

	"github.com/netbja/agent-bus-monitor/agentstate"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/loginexpiry"
	"github.com/netbja/agent-bus-monitor/quota"
)

// serviceLoginExpiry is the dispatch service's login-expiry pass (login-updates
// 2 and r9 §3).
//   - After expiry: Claude Code's transcripts, in its config dir
//     (CLAUDE_CONFIG_DIR, else ~/.claude), the account's every Claude agent
//     shares. No home and no dir: not read.
//   - Before expiry: one field of each Claude agent's credentials, in the
//     agent's own config dir ("" its default, ~/.claude), loginexpiry.Read.
//     The warning threshold is dispatch.login_expiry_warn, read at each pass.
func serviceLoginExpiry(getenv func(string) string) dispatch.LoginConfig {
	home := getenv("HOME")
	c := dispatch.LoginConfig{
		Credentials: func(dir string, now time.Time) (time.Time, loginexpiry.Reason) {
			return loginexpiry.Read(runtime.GOOS, dir, home, loginexpiry.RunCommand, now)
		},
		Checked: func(client string) bool { return agentstate.LoginSourceOf(client) == agentstate.LoginClaudeCredentials },
		Warn:    func() time.Duration { return config.Machine(getenv).Duration("dispatch.login_expiry_warn") },
	}
	dir := getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		if home == "" {
			return c
		}
		dir = filepath.Join(home, ".claude")
	}
	root := filepath.Join(dir, "projects")
	c.ClaudeExpired = func(now time.Time) (time.Time, bool) { return quota.ClaudeLoginExpired(root, now) }
	return c
}
