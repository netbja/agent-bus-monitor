package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/personmem"
)

// The Memory page, F7 (spec 2026-11-21-person-memory-design r2, §4.4, step
// 4 part a: reading): the person's facts on this host, a topic a row with its
// kind, its count and its newest quote; Enter opens a topic. The files live
// on the host: elsewhere (busmon through a tunnel) the page says where, and
// shows nothing (the person's answer 3). Editing, forgetting and adding
// (e, x, a) are part b (memory_actions.go).

// memTopic is one topic as read: its observations, or why it does not parse.
type memTopic struct {
	name, kind string
	obs        []personmem.Observation
	bad        string
}

// memoryView is the page's read: here or not, and the topics.
type memoryView struct {
	local  bool
	host   string // the host's name, when known
	err    string // the store could not be read
	topics []memTopic
}

// memoryItem is a selectable row: its topic, its observation on a topic's
// page, and its body line.
type memoryItem struct {
	topic, id string
	line      int
}

// memoryHere says whether busmon runs on the host of the person's memory:
// the machine the project's services report, else the one holding the
// project's checkout; host is the services' machine when known.
func memoryHere(recs map[string]bus.ServiceRecord, me string, checkout bool) (bool, string) {
	names := make([]string, 0, len(recs))
	for k := range recs {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if h := recs[k].Host; h != "" {
			return h == me, h
		}
	}
	return checkout, ""
}

// readMemory reads the store when local; nothing otherwise.
func readMemory(getenv func(string) string, local bool, host string) memoryView {
	v := memoryView{local: local, host: host}
	if !local {
		return v
	}
	s, err := personmem.Open(getenv)
	if err != nil {
		v.err = err.Error()
		return v
	}
	names, err := s.Topics()
	if err != nil {
		v.err = err.Error()
		return v
	}
	for _, n := range names {
		t, err := s.Load(n)
		if err != nil {
			v.topics = append(v.topics, memTopic{name: n, bad: "does not parse (agentbus mem person check)"})
			continue
		}
		v.topics = append(v.topics, memTopic{name: n, kind: t.Kind, obs: t.Obs})
	}
	return v
}

// memoryPage is F7: its title, the rows, and the selectable topics.
func memoryPage(v memoryView, sel, width int) (title string, body []Line, items []memoryItem) {
	title = "Memory " + GlyphNone + " the person's facts"
	if !v.local {
		where := "the bus's host"
		if v.host != "" {
			where = v.host
		}
		return title, []Line{
			{S(RoleMuted, "The person's memory lives on "+where+": its files are there, and busmon here cannot read them.")},
			{S(RoleMuted, "Open busmon on "+where+", or run agentbus mem person there.")},
		}, nil
	}
	if v.err != "" {
		return title, []Line{{S(RoleError, "the memory cannot be read: "+Preview(v.err))}}, nil
	}
	if len(v.topics) == 0 {
		return title, []Line{{S(RoleMuted, "nothing remembered yet: master files the person's own words with agentbus mem person remember, and a here adds yours")}}, nil
	}
	title += fmt.Sprintf(" %s %d topics", GlyphNone, len(v.topics))
	nameW := 0
	for _, t := range v.topics {
		nameW = max(nameW, Width(t.name))
	}
	for i, t := range v.topics {
		mark := "  "
		if i == sel {
			mark = GlyphSelect + " "
		}
		l := Line{S(RoleAccent, mark), S(RoleText, Preview(t.name)+strings.Repeat(" ", nameW-Width(t.name)+2))}
		if t.bad != "" {
			l = append(l, S(RoleAttention, t.bad))
		} else {
			l = append(l, S(RoleMuted, fmt.Sprintf("%-9s %3d  ", t.kind, len(t.obs))))
			if n := len(t.obs); n > 0 {
				l = append(l, S(RoleText, "\""+Preview(t.obs[n-1].Quote)+"\""))
			}
		}
		items = append(items, memoryItem{topic: t.name, line: len(body)})
		body = append(body, FitLine(l, width))
	}
	return title, body, items
}

// memoryTopicPage is one topic: each observation's filing, its quote, who
// said it, and the question it answers; the observation sel is marked (x).
func memoryTopicPage(v memoryView, topic string, sel, width int) (title string, body []Line, items []memoryItem) {
	title = "Memory " + GlyphNone + " " + Preview(topic)
	for _, t := range v.topics {
		if t.name != topic {
			continue
		}
		if t.bad != "" {
			return title, []Line{{S(RoleAttention, t.bad)}}, nil
		}
		body = append(body, Line{S(RoleMuted, "kind "+t.kind+fmt.Sprintf(", %d observations", len(t.obs)))}, nil)
		for i, o := range t.obs {
			mark := "  "
			if i == sel {
				mark = GlyphSelect + " "
			}
			items = append(items, memoryItem{topic: topic, id: o.ID, line: len(body)})
			body = append(body, FitLine(Line{S(RoleAccent, mark+o.Type), S(RoleMuted, " "+GlyphNone+" "+o.Importance+"  "+o.ID)}, width))
			body = append(body, wrapSpans([]mdSpan{{text: "\"" + Preview(o.Quote) + "\"", role: RoleText}}, width, "  ", "  ")...)
			if o.Question != "" {
				body = append(body, wrapIndented("answers: "+Preview(o.Question), width, "  ")...)
			}
			if o.Supersedes != "" {
				body = append(body, Line{S(RoleMuted, "  supersedes "+o.Supersedes)})
			}
			body = append(body, Line{S(RoleMuted, "  "+Preview(o.Said))}, nil)
		}
		return title, body, items
	}
	return title, []Line{{S(RoleMuted, "no topic "+Preview(topic))}}, nil
}

// memoryLocalOf is where the page reads: the test's answer, else the rule
// on this machine.
func (m *Model) memoryLocalOf() (bool, string) {
	if m.cfg.memoryLocal != nil {
		return m.cfg.memoryLocal()
	}
	me, _ := os.Hostname()
	getenv := m.cfg.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	return memoryHere(m.snap.svcRecs, me, projectCheckout(m.cfg.project, getenv) != "")
}
