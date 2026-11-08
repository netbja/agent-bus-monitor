package main

import (
	"context"
	"os"
	"slices"

	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// idle-implementer's reader (spec docs/superpowers/specs/2026-11-08-idle-implementer-design.md
// §2): the team as the stall guards read it, the quota holds by agent, the
// roles with the implement skill and the board's requested tasks by target.
// The judgement is health.EvaluateIdleImplementer.

func (s *stallReader) idleImplementer(ctx context.Context) (health.Outcome, error) {
	now := s.now()
	set := readStallSettings(s.getenv)
	in := health.IdleImplementerInput{Now: now, After: set.idle}
	defer s.keep(func() { s.lastIdle = &in }) // every return: never a stale wake
	if in.After <= 0 {
		return health.EvaluateIdleImplementer(in), nil
	}
	fail := func(phrase string) (health.Outcome, error) {
		in.ReadErr = phrase
		return health.EvaluateIdleImplementer(in), nil
	}
	read := s.implementers
	if read == nil {
		read = manifestImplementers
	}
	impl, err := read()
	if err != nil {
		return fail("role manifest unavailable")
	}
	in.Implementers = impl
	tr, phrase := s.readTeam(ctx, now, set)
	if phrase != "" {
		return fail(phrase)
	}
	in.Agents, in.Held = tr.agents, map[string]bool{}
	for name, snap := range tr.snaps {
		if tr.held[snap.Provider] {
			in.Held[name] = true
		}
	}
	board, err := s.b.Board(ctx)
	if err != nil {
		return fail("board unavailable")
	}
	in.Requested = map[string][]string{}
	for task, e := range board {
		if e.State != "requested" { // accepted, delivered or done: not passed over
			continue
		}
		// A request's owner is its target (bus/requests.go writes both from one argument).
		in.Requested[e.Owner] = append(in.Requested[e.Owner], task)
	}
	return health.EvaluateIdleImplementer(in), nil
}

// manifestImplementers are the manifest's roles whose skills include
// implement, sorted.
func manifestImplementers() ([]string, error) {
	path, err := manifestPath(os.Environ())
	if err != nil {
		return nil, err
	}
	m, err := rolecfg.Load(path)
	if err != nil {
		return nil, err
	}
	return rolesWithSkill(m, "implement"), nil
}

// rolesWithSkill are m's roles that resolve with skill, sorted.
func rolesWithSkill(m *rolecfg.Manifest, skill string) []string {
	var out []string
	for name := range m.Roles {
		if p, err := m.Resolve(name); err == nil && slices.Contains(p.Skills, skill) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}
