package main

import (
	"context"
	"os"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// An agent's presence in busmon (cleaning-up-agents §3, §10, step 1b, spec
// docs/superpowers/specs/2026-11-26-cleaning-up-agents-design.md): judged by
// bus.Presence from the host facts (its terminal) and what it owes, read on
// a slower tick than the host facts (the gates are scanned per agent).

// presenceFacts are an agent's presence inputs besides its terminal.
type presenceFacts struct {
	owes       bus.Owes
	owesRead   bool // owes were read for it (bus.ReadOwes)
	required   bool // roles.toml says required
	rolesRead  bool // roles.toml was read
	selfRepair bool // dispatch.self_repair is on
}

// presenceEvery is the owes watcher's period.
const presenceEvery = 15 * time.Second

// presenceOf is a's presence and, when lost, why; an input not read yet
// fails closed (lost, "unknown: owes" or "unknown: roles").
func presenceOf(a *agentState) (string, string) {
	p := a.presence
	o := p.owes
	if !p.owesRead {
		o = bus.Owes{Unknown: []string{"owes"}}
	}
	if !p.rolesRead {
		o.Unknown = append([]string{"roles"}, o.Unknown...)
	}
	return bus.Presence(bus.PresenceIn{Pane: a.pane, TermsRead: a.host.termsRead, Live: a.host.live,
		Required: p.required, SelfRepair: p.selfRepair, Owes: o})
}

// owesReader is the bus part the owes need.
type owesReader interface {
	Agents(ctx context.Context) (map[string]bus.AgentSnapshot, error)
	ReadOwes(ctx context.Context, snaps map[string]bus.AgentSnapshot) map[string]bus.Owes
}

// readPresence reads every agent's owes, and the roles file for required.
func readPresence(ctx context.Context, b owesReader, getenv func(string) string) (map[string]presenceFacts, error) {
	snaps, err := b.Agents(ctx)
	if err != nil {
		return nil, err
	}
	owes := b.ReadOwes(ctx, snaps)
	var man *rolecfg.Manifest
	if path, err := rolecfg.ManifestPath(getenv); err == nil && path != "" {
		man, _ = rolecfg.Load(path)
	}
	repair := config.SelfRepairOn(config.Machine(getenv))
	out := make(map[string]presenceFacts, len(owes))
	for n, o := range owes {
		f := presenceFacts{owes: o, owesRead: true, selfRepair: repair}
		if man != nil {
			f.rolesRead, f.required = true, man.RoleRequired(n)
		}
		out[n] = f
	}
	return out, nil
}

// applyPresence writes the facts on the agents busmon knows; one left out
// keeps none read (fail closed).
func applyPresence(st *shared, facts map[string]presenceFacts) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for n, a := range st.agents {
		if a != nil {
			a.presence = facts[n]
		}
	}
}

// watchPresence keeps the presence facts current until ctx ends; a failed
// read keeps the last ones.
func watchPresence(ctx context.Context, st *shared, b owesReader) {
	t := time.NewTicker(presenceEvery)
	defer t.Stop()
	for {
		c, cancel := context.WithTimeout(ctx, presenceEvery)
		if facts, err := readPresence(c, b, os.Getenv); err == nil {
			applyPresence(st, facts)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
