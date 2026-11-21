package personmem

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/netbja/agent-bus-monitor/health"
)

// Finding is one thing check found wrong: the file and the observation, and
// what, never a secret's value.
type Finding struct {
	File string
	ID   string
	What string
}

func (f Finding) String() string {
	if f.ID == "" {
		return f.File + ": " + f.What
	}
	return f.File + ": " + f.ID + ": " + f.What
}

// idRE is an observation's id: the date, then the entry's id or b<ms> from
// busmon, then -N for another statement of the same entry.
var idRE = regexp.MustCompile(`^\d{8}-(?:b\d+|\d+-\d+)(?:-\d+)?$`)

// Check validates every topic file (spec §4.2): the front matter, the ids,
// the names, the quotes and the supersedes targets, and no credential
// shape. A hand edit is checked the same way as a remember.
func (s *Store) Check() []Finding {
	all, errs := s.loadAll()
	var out []Finding
	for n, err := range errs {
		out = append(out, Finding{File: n + ".md", What: err.Error()})
	}
	ids := map[string]bool{}
	for _, t := range all {
		for _, o := range t.Obs {
			ids[o.ID] = true
		}
	}
	names, _ := s.Topics()
	for _, n := range names {
		t := all[n]
		if t == nil {
			continue
		}
		file := n + ".md"
		add := func(id, format string, a ...any) {
			out = append(out, Finding{File: file, ID: id, What: fmt.Sprintf(format, a...)})
		}
		if t.Topic != n {
			add("", "its front matter names topic %q", t.Topic)
		}
		if !slices.Contains(Kinds, t.Kind) {
			add("", "kind %q is not one of %v", t.Kind, Kinds)
		}
		for _, r := range t.Relations {
			if !slices.Contains(RelTypes, r.Type) || !ValidTopic(r.Topic) || health.Sensitive(r.Topic) {
				add("", "a relation is not <%v>: <topic>", RelTypes)
			}
		}
		if len(t.Obs) > MaxObs {
			add("", "%d observations, at most %d", len(t.Obs), MaxObs)
		}
		seen := map[string]bool{}
		for _, o := range t.Obs {
			switch {
			case !idRE.MatchString(o.ID):
				add(o.ID, "bad id (<YYYYMMDD>-<entry id>)")
			case seen[o.ID]:
				add(o.ID, "the id is there twice")
			}
			seen[o.ID] = true
			if !slices.Contains(Types, o.Type) {
				add(o.ID, "type %q is not one of %v", o.Type, Types)
			}
			if !slices.Contains(Importances, o.Importance) {
				add(o.ID, "importance %q is not one of %v", o.Importance, Importances)
			}
			if o.Said == "" {
				add(o.ID, "no said line")
			}
			if err := quoteShape("the quote", o.Quote); err != nil {
				add(o.ID, "%v", err)
			}
			for _, f := range []struct{ name, v string }{{"the quote", o.Quote}, {"the question", o.Question}} {
				if health.Sensitive(f.v) {
					add(o.ID, "%s holds a credential shape", f.name)
				}
			}
			if o.Supersedes != "" && !ids[o.Supersedes] {
				add(o.ID, "supersedes %s: no such observation", o.Supersedes)
			}
		}
	}
	slices.SortFunc(out, func(a, b Finding) int {
		if a.File != b.File {
			if a.File < b.File {
				return -1
			}
			return 1
		}
		return 0
	})
	return out
}
