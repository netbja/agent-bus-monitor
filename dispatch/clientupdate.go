package dispatch

import (
	"context"
	"fmt"
	"maps"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/clientfp"
)

// Client updates (spec 2026-10-21-client-reload-design.md): the observe pass
// tells whether the client an agent runs was updated on disk since it
// started, from file metadata only, and sentinel gets one directive per
// update episode. Nothing here runs a client, types, restarts or reads a
// credential.

// probeEvery bounds the probe by default: one look per agent and minute.
const probeEvery = time.Minute

type probeResult struct {
	at     time.Time
	pid    int
	update *bus.ClientUpdate
	known  bool
}

// probeClient is the throttled probe for one agent.
func (l *loop) probeClient(agent string, s bus.AgentSnapshot) (*bus.ClientUpdate, bool) {
	if l.probes == nil {
		l.probes = map[string]probeResult{}
	}
	every := l.cfg.ClientProbeEvery
	if every <= 0 {
		every = probeEvery
	}
	if p, ok := l.probes[agent]; ok && p.pid == s.PID && time.Since(p.at) < every {
		return copyUpdate(p.update), p.known
	}
	probe := l.cfg.ClientProbe
	if probe == nil {
		probe = realClientProbe
	}
	u, known := probe(s)
	l.probes[agent] = probeResult{at: time.Now(), pid: s.PID, update: copyUpdate(u), known: known}
	return u, known
}

func copyUpdate(u *bus.ClientUpdate) *bus.ClientUpdate {
	if u == nil {
		return nil
	}
	c := *u
	return &c
}

// realClientProbe: the launch fingerprint when the agent has one (any
// client), else the Claude Code layout of its running executable (Linux).
func realClientProbe(s bus.AgentSnapshot) (*bus.ClientUpdate, bool) {
	if fp, err := clientfp.Decode(s.ClientFP); err == nil {
		changed, known := fp.Changed()
		if !known || !changed {
			return nil, known
		}
		now, err := clientfp.Take(fp.Path)
		if err != nil {
			return nil, false
		}
		return &bus.ClientUpdate{Client: s.Client, Running: fp.Label, Installed: now.Label, Hash: now.Hash(), Source: "fingerprint"}, true
	}
	if runtime.GOOS != "linux" || s.PID <= 0 || s.Client != "" && s.Client != "claude-code" {
		return nil, false
	}
	exe, err := os.Readlink("/proc/" + strconv.Itoa(s.PID) + "/exe")
	if err != nil {
		return nil, false
	}
	running, installed, outdated, ok := clientfp.ClaudeLayout(exe)
	if !ok {
		return nil, false
	}
	if !outdated {
		return nil, true
	}
	return &bus.ClientUpdate{Client: "claude-code", Running: running, Installed: installed, Hash: "layout-" + installed, Source: "layout"}, true
}

// proposeReloads sends sentinel (else the escalation target) one directive
// per update episode, with its once-mark in the same fenced script: the mark
// update:<client> holds the installed version's hash, so a restarted
// dispatcher, another pass or another agent on the same update sends nothing.
func (l *loop) proposeReloads(ctx context.Context, snaps map[string]bus.AgentSnapshot, obs map[string]Observation) error {
	byEpisode := map[[2]string][]string{}
	labels := map[[2]string]*bus.ClientUpdate{}
	for a, o := range obs {
		if o.Update == nil {
			continue
		}
		k := [2]string{o.Update.Client, o.Update.Hash}
		byEpisode[k] = append(byEpisode[k], a)
		labels[k] = o.Update
	}
	target := l.cfg.EscalateTo
	if _, ok := snaps["sentinel"]; ok {
		target = "sentinel"
	}
	for _, k := range slices.SortedFunc(maps.Keys(byEpisode), func(a, b [2]string) int {
		return strings.Compare(a[0]+"\x00"+a[1], b[0]+"\x00"+b[1])
	}) {
		agents := byEpisode[k]
		slices.Sort(agents)
		u := labels[k]
		client := k[0]
		if client == "" {
			client = "the client"
		}
		msg := fmt.Sprintf("client update: %s %s installed; still running %s: %s. Propose a reload (agent-bus-sentinel skill, Client update): propose only, never restart or type.",
			client, orUnknown(u.Installed), orUnknown(u.Running), strings.Join(agents, ", "))
		if _, err := l.b.FencedCmdOnce(ctx, l.token, "update:"+k[0], k[1], dispatchFrom, target, bus.CmdDirective, truncRunes(msg, bus.CmdMaxRunes), false); err != nil {
			return err
		}
	}
	return nil
}

func orUnknown(s string) string {
	if s == "" {
		return "(version unknown)"
	}
	return s
}
