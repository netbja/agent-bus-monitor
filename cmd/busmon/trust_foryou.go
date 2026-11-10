package main

import (
	"os"
	"strings"

	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/projlist"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/trust"
)

// first-run-trust (b), step 2: an agent herdr restored (no agentbus up ran,
// so no reminder ask) stops on its client's "do you trust this folder?". Its
// For you prompt row then says which folder and what to choose. busmon reads
// only the clients' trust flags (package trust), never writes them, and only
// for an agent whose process runs on this host: another host's agents
// (reached through a tunnel or a remote broker) have their clients' files
// there, not here, and keep today's "pane prompt".

// trustDeps is what the trust line reads, injected for tests.
type trustDeps struct {
	procStart  func(pid int) (uint64, error) // dispatch.ProcStart: the process's start on this host
	workingDir func() (string, bool)         // the project template's working_dir, expanded
	trusted    func(client, dir string) bool // trust.Reader
}

// folder is the folder client's dialog is about when it does not trust it
// yet (trust.Key: the repository's root), for the process pid started at
// start on this host; "" otherwise: a trusted folder, a client whose trust
// gates nothing, a process not here (or not the one recorded), no template.
func (d trustDeps) folder(client string, pid int, start uint64) string {
	if !trust.Gated[client] || pid <= 0 || start == 0 {
		return ""
	}
	if s, err := d.procStart(pid); err != nil || s != start {
		return ""
	}
	dir, ok := d.workingDir()
	if !ok || d.trusted(client, dir) {
		return ""
	}
	key, _ := trust.Key(dir)
	return key
}

// templateWorkingDir reads the project's template (projlist) for its
// working_dir, expanded as agentbus up expands it (~ then $VARS).
func templateWorkingDir(project string, env func(string) string) func() (string, bool) {
	return func() (string, bool) {
		rows, err := projlist.Templates(rolecfg.ProjectsDir(env))
		if err != nil {
			return "", false
		}
		for _, r := range rows {
			if r.Name == project && r.WorkingDir != "" {
				return os.Expand(strings.Replace(r.WorkingDir, "~", env("HOME"), 1), env), true
			}
		}
		return "", false
	}
}

// localTrustOf is the trust line's reader for project on this host.
func localTrustOf(project string, env func(string) string) func(client string, pid int, start uint64) string {
	return trustDeps{procStart: dispatch.ProcStart, workingDir: templateWorkingDir(project, env), trusted: trust.Reader(env)}.folder
}

// trustPromptText is the prompt row's text for an untrusted folder: the
// client, the folder, the choice by its label.
func trustPromptText(client, folder string) string {
	return trust.Label(client) + " asks whether you trust " + folder + `: choose "` + trust.Choice + `"`
}
