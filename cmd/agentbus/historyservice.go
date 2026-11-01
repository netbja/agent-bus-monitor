package main

import (
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/history"
)

// serviceHistory is the dispatch service's archive chore (history spec):
// history.keep_days (0 = no archive) and history.max_mb, the files in the
// project's state directory.
func serviceHistory(getenv func(string) string, stateDir string) dispatch.HistoryConfig {
	r := config.Machine(getenv)
	days := r.Int("history.keep_days")
	if days <= 0 || stateDir == "" {
		return dispatch.HistoryConfig{}
	}
	return dispatch.HistoryConfig{Dir: history.Dir(stateDir), KeepDays: days, MaxBytes: int64(r.Int("history.max_mb")) << 20}
}
