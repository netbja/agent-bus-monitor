package main

import (
	"fmt"
	"io"

	"github.com/netbja/agent-bus-monitor/config"
)

// settingsWarnings are the warnings of the settings files a process whose
// environment is getenv reads (config.toml, busmon.toml): a bad value falls
// back to the next source, and says so here.
func settingsWarnings(getenv func(string) string) []string {
	var out []string
	for _, w := range config.Machine(getenv).Warnings() {
		out = append(out, w.String())
	}
	return out
}

// printSettingsWarnings writes each warning once, for a command run by hand
// or by an agent; the services log theirs at start instead.
func printSettingsWarnings(getenv func(string) string, w io.Writer) {
	for _, line := range settingsWarnings(getenv) {
		fmt.Fprintln(w, "agentbus: settings: "+line)
	}
}
