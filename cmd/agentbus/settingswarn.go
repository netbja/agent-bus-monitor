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
	r := config.Machine(getenv)
	for _, w := range r.Warnings() {
		out = append(out, w.String())
	}
	// An env value its rule skips: named by its variable, never its value,
	// with the source that applies instead (as agentbus config says it).
	for _, k := range config.Keys {
		if k.Env == "" || getenv(k.Env) == "" {
			continue
		}
		if g := r.Get(k.Name); !g.Invalid && g.Source != "env "+k.Env {
			out = append(out, fmt.Sprintf("env %s: set but not a value its rule takes; %s applies", k.Env, g.Source))
		}
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
