// Package onboard holds what agentbus init and plan draft read from an
// onboarding's files (spec
// docs/superpowers/specs/2026-11-27-project-onboarding-design.md).
package onboard

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/netbja/agent-bus-monitor/plan"
)

// Build is what BUILD.md says that a command reads: the project and the
// sheet's version from its title, the goal, and the definition of done.
type Build struct {
	Project string
	Version string // "v0", "v0.1", …
	Goal    string
	DoD     []DoDItem
}

// DoDItem is one item of the definition of done, with its check (§5.4).
type DoDItem struct {
	ID    string // dod-<k>
	Item  string
	Check string
}

// Headings are BUILD.md's fixed headings, in their order (§6.4). Others
// (the grade's, "Done before v<n>") may stand anywhere.
var Headings = []string{"Goal", "Users", "Constraints", "Stack", "Definition of done",
	"Out of scope", "How to run", "How to verify", "Risks", "Decisions"}

var (
	titleRe = regexp.MustCompile(`^# (\S.*): build sheet \((v[0-9]+(?:\.[0-9]+)*)\)$`)
	itemRe  = regexp.MustCompile(`^- \[(dod-[0-9]+)\] (\S.*?) — check: (.*)$`)
)

// ParseBuild reads BUILD.md (§6.4). It refuses a sheet whose first line is
// not its title, that lacks one of the fixed headings or holds one twice or
// out of order, an empty goal, and a definition of done with no item, a
// line that is not an item, an item without "— check:", a check plan.Parse
// would refuse, or an id twice.
func ParseBuild(data []byte) (Build, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	m := titleRe.FindStringSubmatch(lines[0])
	if m == nil {
		return Build{}, errors.New(`BUILD.md: the first line must be "# <project>: build sheet (v<n>)"`)
	}
	b := Build{Project: m[1], Version: m[2]}
	sections := map[string][]string{}
	next := 0 // the next fixed heading expected
	cur := ""
	for _, l := range lines[1:] {
		if h, ok := strings.CutPrefix(l, "## "); ok {
			h = strings.TrimSpace(h)
			cur = h
			if _, seen := sections[h]; seen {
				return Build{}, fmt.Errorf("BUILD.md: the heading %q is there twice", h)
			}
			sections[h] = []string{}
			if i := indexOf(Headings, h); i >= 0 {
				if i != next {
					return Build{}, fmt.Errorf("BUILD.md: %q must come after %q", h, Headings[next])
				}
				next++
			}
			continue
		}
		if cur != "" {
			sections[cur] = append(sections[cur], l)
		}
	}
	if next < len(Headings) {
		return Build{}, fmt.Errorf("BUILD.md: no heading %q", "## "+Headings[next])
	}
	b.Goal = strings.TrimSpace(strings.Join(sections["Goal"], "\n"))
	if b.Goal == "" {
		return Build{}, errors.New("BUILD.md: the goal is empty")
	}
	ids := map[string]bool{}
	for _, l := range sections["Definition of done"] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		m := itemRe.FindStringSubmatch(l)
		if m == nil {
			return Build{}, fmt.Errorf("BUILD.md: %q is not \"- [dod-<k>] <item> — check: <check>\"", l)
		}
		if ids[m[1]] {
			return Build{}, fmt.Errorf("BUILD.md: %s is there twice", m[1])
		}
		ids[m[1]] = true
		if !plan.ValidCheck(m[3]) {
			return Build{}, fmt.Errorf("BUILD.md: %s's check must be one line of 1 to 200 characters, none of them a control or format character", m[1])
		}
		b.DoD = append(b.DoD, DoDItem{ID: m[1], Item: m[2], Check: m[3]})
	}
	if len(b.DoD) == 0 {
		return Build{}, errors.New("BUILD.md: the definition of done has no item")
	}
	return b, nil
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
