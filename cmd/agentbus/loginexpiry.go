package main

import (
	"path/filepath"
	"time"

	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/quota"
)

// serviceLoginExpiry is the dispatch service's login-expiry pass (login-updates
// 2): Claude Code's transcripts, in its config dir (CLAUDE_CONFIG_DIR, else
// ~/.claude), the account's every Claude agent shares. No home: not read.
func serviceLoginExpiry(getenv func(string) string) dispatch.LoginConfig {
	dir := getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home := getenv("HOME")
		if home == "" {
			return dispatch.LoginConfig{}
		}
		dir = filepath.Join(home, ".claude")
	}
	root := filepath.Join(dir, "projects")
	return dispatch.LoginConfig{ClaudeExpired: func(now time.Time) (time.Time, bool) { return quota.ClaudeLoginExpired(root, now) }}
}
