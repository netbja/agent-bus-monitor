package mission

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

var missionIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,7}$`)

const maxChangeLen = 16

// Validate applies every §3.2 rule and reports all the problems at once.
// tree is the repository's file list at the loaded commit; brief resolves a
// brief path (relative to the change folder, anchor removed) and says whether
// it exists at that commit. Validate itself does no I/O.
func Validate(f File, tree []string, brief func(path string) (resolved string, ok bool)) error {
	var probs []string
	bad := func(format string, a ...any) { probs = append(probs, fmt.Sprintf(format, a...)) }

	if !bus.ValidName(f.Change) || len(f.Change) > maxChangeLen {
		bad("change %q: lowercase name of at most %d characters", f.Change, maxChangeLen)
	}
	if f.Branch != "change/"+f.Change {
		bad("branch %q: must be change/%s (the conductor writes only there)", f.Branch, f.Change)
	}
	if f.Base == "" || f.Base == f.Branch {
		bad("base %q: must name another branch than %s", f.Base, f.Branch)
	}
	if !bus.ValidName(f.Reviewer) {
		bad("reviewer %q: a default reviewer is required", f.Reviewer)
	}
	if f.Docs != "" && !bus.ValidName(f.Docs) {
		bad("docs %q: not a valid agent name", f.Docs)
	}
	if f.MaxAttempts < 1 || f.MaxAttempts > 99 {
		bad("max_attempts %d: between 1 and 99", f.MaxAttempts)
	}
	if len(f.Missions) == 0 {
		bad("no missions")
	}

	ids := map[string]bool{}
	for _, m := range f.Missions {
		switch {
		case !missionIDRE.MatchString(m.ID):
			bad("mission %q: id must match %s", m.ID, missionIDRE)
		case ids[m.ID]:
			bad("mission %q: duplicate id", m.ID)
		case !bus.ValidName(TaskName(f.Change, m.ID, 99)):
			bad("mission %q: request name %s is not a valid bus name", m.ID, TaskName(f.Change, m.ID, 99))
		}
		ids[m.ID] = true
		if strings.TrimSpace(m.Title) == "" {
			bad("mission %q: title required", m.ID)
		}
		if !bus.ValidName(m.Agent) {
			bad("mission %q: invalid agent %q", m.ID, m.Agent)
		}
		if r := f.ReviewerOf(m); !bus.ValidName(r) || r == m.Agent {
			bad("mission %q: reviewer %q must be a valid name and not the agent", m.ID, r)
		}
		if len(m.AllowedPaths) == 0 {
			bad("mission %q: allowed_paths required", m.ID)
		}
		for _, p := range m.AllowedPaths {
			if _, err := path.Match(strings.TrimSuffix(p, "/**"), ""); err != nil || p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
				bad("mission %q: bad allowed path %q", m.ID, p)
			}
		}
		if strings.TrimSpace(m.Test) == "" {
			bad("mission %q: test command required", m.ID)
		}
		if m.Timeout != "" {
			if d, err := time.ParseDuration(m.Timeout); err != nil || d <= 0 {
				bad("mission %q: timeout %q is not a duration", m.ID, m.Timeout)
			}
		}
		name, _, _ := strings.Cut(m.Brief, "#")
		if name == "" {
			bad("mission %q: brief required", m.ID)
		} else if resolved, ok := brief(name); !ok {
			bad("mission %q: brief %q (resolved: %s) not found at the loaded commit", m.ID, name, resolved)
		}
	}
	for _, m := range f.Missions {
		for _, d := range m.DependsOn {
			if !ids[d] {
				bad("mission %q: unknown dependency %q", m.ID, d)
			}
		}
	}
	if c := findCycle(f); c != "" {
		bad("dependency cycle through %q", c)
	}
	if len(probs) == 0 { // overlap needs a sane graph
		for i, a := range f.Missions {
			for _, b := range f.Missions[i+1:] {
				if !DependsTransitively(f, a.ID, b.ID) && !DependsTransitively(f, b.ID, a.ID) && Overlap(a.AllowedPaths, b.AllowedPaths, tree) {
					bad("missions %q and %q: allowed_paths overlap without a dependency between them", a.ID, b.ID)
				}
			}
		}
	}
	if len(probs) > 0 {
		return errors.New("missions.json: " + strings.Join(probs, "; "))
	}
	return nil
}

// DependsTransitively reports whether a depends on b, directly or not.
func DependsTransitively(f File, a, b string) bool {
	seen := map[string]bool{}
	var walk func(id string) bool
	walk = func(id string) bool {
		if seen[id] {
			return false
		}
		seen[id] = true
		m, ok := f.Mission(id)
		if !ok {
			return false
		}
		for _, d := range m.DependsOn {
			if d == b || walk(d) {
				return true
			}
		}
		return false
	}
	return walk(a)
}

// findCycle returns a mission on a dependency cycle, or "".
func findCycle(f File) string {
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var visit func(id string) string
	visit = func(id string) string {
		color[id] = grey
		m, _ := f.Mission(id)
		for _, d := range m.DependsOn {
			switch color[d] {
			case grey:
				return d
			case white:
				if _, ok := f.Mission(d); ok {
					if c := visit(d); c != "" {
						return c
					}
				}
			}
		}
		color[id] = black
		return ""
	}
	for _, m := range f.Missions {
		if color[m.ID] == white {
			if c := visit(m.ID); c != "" {
				return c
			}
		}
	}
	return ""
}
