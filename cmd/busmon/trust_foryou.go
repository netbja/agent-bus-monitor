package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
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
// there, not here, and keep today's "pane prompt". "On this host" is the
// recorded pid running here with its recorded start time AND an environment
// naming this project and this agent, as the launcher exports them: pid and
// start (ticks since this kernel's boot) alone could match a remote agent by
// chance (reviewer-codex).

// trustDeps is what the trust line reads, injected for tests.
type trustDeps struct {
	project    string                                     // busmon's project: the process must be its agent
	procStart  func(pid int) (uint64, error)              // dispatch.ProcStart: the process's start on this host
	procEnv    func(pid int) (func(string) string, error) // the process's environment (procEnviron)
	workingDir func() (string, bool)                      // the project template's working_dir, expanded
	trusted    func(client, dir string) bool              // trust.Reader
}

// folder is the folder client's dialog is about when it does not trust it
// yet (trust.Key: the repository's root), for agent's process pid started at
// start on this host; "" otherwise: a trusted folder, a client whose trust
// gates nothing, a process not here (or not the one recorded, or not this
// project's agent of that name), no template.
func (d trustDeps) folder(agent, client string, pid int, start uint64) string {
	if !trust.Gated[client] || pid <= 0 || start == 0 {
		return ""
	}
	if s, err := d.procStart(pid); err != nil || s != start {
		return ""
	}
	env, err := d.procEnv(pid)
	if err != nil || env("AGENT_BUS_PROJECT") != d.project || env("AGENT_BUS_AGENT") != agent {
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
func localTrustOf(project string, env func(string) string) func(agent, client string, pid int, start uint64) string {
	return trustDeps{project: project, procStart: dispatch.ProcStart, procEnv: procEnviron,
		workingDir: templateWorkingDir(project, env), trusted: trust.Reader(env)}.folder
}

// procEnviron reads the environment pid was started with (Linux:
// /proc/<pid>/environ, readable for the same user). Elsewhere, or for a
// process of another user, it fails: no trust line. Only the launcher's
// AGENT_BUS_ names are kept.
func procEnviron(pid int) (func(string) string, error) {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil {
		return nil, err
	}
	vars := map[string]string{}
	for _, kv := range bytes.Split(b, []byte{0}) { // no string of the whole environment: it may hold secrets
		if k, v, ok := bytes.Cut(kv, []byte("=")); ok && bytes.HasPrefix(k, []byte("AGENT_BUS_")) {
			vars[string(k)] = string(v)
		}
	}
	clear(b)
	return func(k string) string { return vars[k] }, nil
}

// trustPromptText is the prompt row's text for an untrusted folder: the
// client, the folder, the choice by its label.
func trustPromptText(client, folder string) string {
	return trust.Label(client) + " asks whether you trust " + folder + `: choose "` + trust.Choice + `"`
}
