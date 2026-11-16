package main

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/service"
)

// holderProbe reads the holder sockets of a project: which build keys have
// one (and since when), and whether each answers, with its terminals.
type holderProbe struct {
	sockets func(project string) (map[string]time.Duration, error)
	answer  func(project, key string) (terms []holder.TermInfo, ok bool)
}

// realHolderProbe lists the project's holder sockets (holder.ProjectSockets: h-<id>.sock, or an older build's holder-<key>.sock; the
// directory checked, holder.ProjectDir) and asks each over its socket.
func realHolderProbe(getenv func(string) string) holderProbe {
	return holderProbe{
		sockets: func(project string) (map[string]time.Duration, error) {
			dir, err := holder.ProjectDir(getenv, project)
			if err != nil {
				return nil, err
			}
			socks, err := holder.ProjectSockets(dir) // both names (P0-2 B)
			if err != nil {
				return nil, err
			}
			out := map[string]time.Duration{}
			for key, sock := range socks {
				if info, err := os.Lstat(sock); err == nil {
					out[key] = time.Since(info.ModTime())
				}
			}
			return out, nil
		},
		answer: func(project, key string) ([]holder.TermInfo, bool) {
			terms, err := holderTermList(project, key)
			return terms, err == nil
		},
	}
}

// holderEvidence is every build of project's holder, by key: its unit and
// whether the manager runs it, its socket (answering, terminals, age), its
// binary copy. A binary copy that another project's unit records is that
// project's build, never this one's leftover. A manager that cannot say
// makes the whole evidence unknown.
func holderEvidence(m service.Manager, paths service.Paths, project, currentRaw string, probe holderProbe) health.HolderEvidence {
	builds := map[string]*health.HolderBuild{}
	get := func(key string) *health.HolderBuild {
		if b, ok := builds[key]; ok {
			return b
		}
		b := &health.HolderBuild{Key: key, Raw: key}
		builds[key] = b
		return b
	}
	current := service.BuildKey(currentRaw)
	get(current).Raw, get(current).Current = currentRaw, true
	others := map[string]bool{} // keys another project's unit runs
	if hm, ok := m.(service.HolderManager); ok {
		all, err := hm.HolderUnits("")
		if err != nil {
			return health.HolderEvidence{}
		}
		for _, u := range all {
			if u.Project != project {
				others[u.Key] = true
				continue
			}
			on, err := hm.HolderActive(project, u.Key)
			if err != nil {
				return health.HolderEvidence{}
			}
			b := get(u.Key)
			b.Raw, b.Unit, b.Active = u.Raw, true, on
		}
	}
	socks, err := probe.sockets(project)
	if err != nil {
		return health.HolderEvidence{}
	}
	for key, age := range socks {
		if terms, ok := probe.answer(project, key); ok {
			b := get(key)
			b.Answers, b.Terms, b.Since = true, len(terms), age
		}
	}
	base := filepath.Join(paths.DataHome, "agentbus", "holder")
	if ents, err := os.ReadDir(base); err == nil {
		for _, d := range ents {
			key := d.Name()
			if !d.IsDir() || !service.ValidBuildKey(key) {
				continue
			}
			if _, mine := builds[key]; !mine && others[key] {
				continue
			}
			b := get(key)
			b.Binary = true
			if raw, err := os.ReadFile(filepath.Join(base, key, "build-raw")); err == nil && !b.Unit && !b.Current {
				b.Raw = strings.TrimSpace(string(raw))
			}
		}
	} else if !os.IsNotExist(err) {
		return health.HolderEvidence{}
	}
	e := health.HolderEvidence{Known: true}
	keys := make([]string, 0, len(builds))
	for k := range builds {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b := *builds[k]
		if !b.Current && !b.Unit && !b.Answers && !b.Binary {
			continue
		}
		e.Builds = append(e.Builds, b)
	}
	return e
}

// holderAnswersAny: a holder of build key answers for some project of this
// host (its socket, either name: holder.SocketFor): its binary copy is in use.
func holderAnswersAny(getenv func(string) string, key string) bool {
	root, err := holder.RunAgentbusDir(getenv)
	if err != nil {
		return true // cannot tell: keep the copy
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return true
	}
	for _, d := range ents {
		if !d.IsDir() || !bus.ValidName(d.Name()) {
			continue
		}
		if _, err := holderTerms(d.Name(), key); err == nil {
			return true
		}
	}
	return false
}

// withHolderAgents is health.WithHolderAgents of the agents' snapshots;
// alive says whether a snapshot's proved process may still run here (false:
// proved gone).
func withHolderAgents(e health.HolderEvidence, snaps map[string]bus.AgentSnapshot, redisNow time.Time, alive func(bus.AgentSnapshot) bool) health.HolderEvidence {
	proofs := make([]health.HolderProof, 0, len(snaps))
	for n, s := range snaps {
		proofs = append(proofs, health.HolderProof{Name: n, Pane: s.Pane, State: s.State, PID: s.PID, IdentTS: s.IdentTS, Alive: alive(s)})
	}
	return health.WithHolderAgents(e, proofs, redisNow)
}

// provedAlive: the snapshot's proved process may still run on this host;
// false only when it is proved gone: another boot (a reboot since the
// proof), no such process (dispatch.ErrNoProcess), or another start time
// for that pid. A lookup that fails otherwise, or a proof without a start
// time, proves nothing, so the agent is never called lost on it
// (reviewer-codex).
func provedAlive(boot string, start func(int) (uint64, error)) func(bus.AgentSnapshot) bool {
	return func(s bus.AgentSnapshot) bool {
		if s.PID <= 0 || s.BootID != "" && boot != "" && s.BootID != boot {
			return false
		}
		if s.PIDStart == 0 { // no start time recorded: no process is identified, so none is proved gone
			return true
		}
		got, err := start(s.PID)
		switch {
		case errors.Is(err, dispatch.ErrNoProcess):
			return false
		case err != nil:
			return true // unknown: not lost
		}
		return got == s.PIDStart
	}
}
