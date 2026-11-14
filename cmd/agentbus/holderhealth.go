package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/service"
)

// holderProbe reads the holder sockets of a project: which build keys have
// one (and since when), and whether each answers, with its terminals.
type holderProbe struct {
	sockets func(project string) (map[string]time.Duration, error)
	answer  func(project, key string) (terms int, ok bool)
}

// realHolderProbe lists <rundir>/agentbus/<project>/holder-<key>.sock (the
// directory checked, holder.ProjectDir) and asks each over its socket.
func realHolderProbe(getenv func(string) string) holderProbe {
	return holderProbe{
		sockets: func(project string) (map[string]time.Duration, error) {
			dir, err := holder.ProjectDir(getenv, project)
			if err != nil {
				return nil, err
			}
			ents, err := os.ReadDir(dir)
			if err != nil {
				return nil, err
			}
			out := map[string]time.Duration{}
			for _, d := range ents {
				key, ok := strings.CutPrefix(d.Name(), "holder-")
				if key, ok = strings.CutSuffix(key, ".sock"); !ok || !service.ValidBuildKey(key) {
					continue
				}
				if info, err := d.Info(); err == nil {
					out[key] = time.Since(info.ModTime())
				}
			}
			return out, nil
		},
		answer: func(project, key string) (int, bool) {
			n, err := holderTerms(project, key)
			return n, err == nil
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
		if n, ok := probe.answer(project, key); ok {
			b := get(key)
			b.Answers, b.Terms, b.Since = true, n, age
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
// host (<rundir>/agentbus/*/holder-<key>.sock): its binary copy is in use.
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
