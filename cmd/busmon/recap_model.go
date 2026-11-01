package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	guardian "github.com/netbja/agent-bus-monitor/health"
)

// recapForYou is the recap's section for the person: shown only when the
// For you panel is not on screen (the person's answer, busmon pages), so
// nothing shows twice (recapForScreen).
const recapForYou = "For you"

type recapRow struct {
	Key, Section, Title, Detail string
	Kind, Agent, Ref, Task      string
	Event                       bus.Event
	Verdict                     *bus.Verdict
	Swap                        *bus.SwapEvent
	Selectable                  bool
}

type recapModel struct {
	Rows            []recapRow
	Now             time.Time
	Since           time.Time
	Target          string
	SummaryDisabled string
	SummarySentAt   time.Time
	SummaryReplied  bool
	SummaryStatus   string
	Warnings        []string
}

type recapInput struct {
	Now         time.Time
	Since       time.Time
	Agents      map[string]*agentState
	Asks        []bus.Ask
	Board       map[string]bus.BoardEntry
	Commands    []bus.Event
	Reports     []bus.Event
	Verdicts    []bus.Verdict
	Swaps       []bus.SwapEvent
	Health      []guardian.Transition
	HealthFloor string
	Services    map[string]bus.ServiceRecord
	Pause       *bus.PauseRecord
	Holds       map[string]int64
	Human       string
	Unread      []bus.Event // entries for the person not opened yet (unread.go), oldest first
	Plan        *planState  // the loaded plan (nil: none); its rows were derived in pollOnce
	Errors      map[string]error
}

func buildRecap(in recapInput) recapModel {
	if in.Now.IsZero() {
		in.Now = time.Now()
	}
	m := recapModel{Now: in.Now, Since: in.Since, Target: recapTarget(in.Commands)}
	add := func(r recapRow) { m.Rows = append(m.Rows, r) }
	section := func(title string) { add(recapRow{Section: title, Title: title}) }
	names := make([]string, 0, len(in.Agents))
	for n := range in.Agents {
		names = append(names, n)
	}
	sort.Strings(names)
	section(recapForYou)
	for _, e := range in.Unread { // the unread marks first (busmon UX item 8)
		add(recapRow{Key: "unread:" + e.ID, Section: recapForYou,
			Title:  sender(e) + " " + GlyphNone + " unread " + GlyphNone + " " + humanAge(max(0, in.Now.Sub(eventTimestamp(e.ID)))),
			Detail: e.Message})
	}
	for _, a := range openAsks(in.Asks) {
		for _, q := range a.Questions {
			if q.Answer == nil {
				add(recapRow{Key: "ask:" + a.ID + ":" + q.ID, Section: recapForYou, Title: a.From + " asks " + GlyphNone + " " + q.Header,
					Detail: q.Question, Kind: "ask", Agent: a.From, Ref: a.ID, Selectable: true})
			}
		}
	}
	for _, n := range names {
		a := in.Agents[n]
		if a.state == "waiting" && !answered(a) {
			add(recapRow{Key: fmt.Sprintf("wait:%s:%d", n, a.stateAt.UnixMilli()), Section: recapForYou,
				Title: n + " waiting " + GlyphNone + " " + humanAge(max(0, in.Now.Sub(a.stateAt))), Detail: a.message,
				Kind: "agent", Agent: n, Selectable: true})
		}
	}
	threadReplies := make(map[string]bool)
	for _, e := range in.Commands {
		if e.Type == bus.CmdReply && e.Ref != "" && e.Human {
			threadReplies[e.Ref] = true
		}
	}
	for _, e := range in.Commands {
		if e.Target != bus.PeopleTarget && (in.Human == "" || e.Target != in.Human) || e.Human || e.Type != bus.CmdDirective && e.Type != bus.CmdEscalation {
			continue
		}
		thread := threadID(e)
		if threadReplies[thread] || in.Now.Sub(eventTimestamp(e.ID)) > bus.DefaultHorizon { // same window as the Requests count
			continue
		}
		add(recapRow{Key: "cmd:" + e.ID, Section: recapForYou, Title: e.From + " " + GlyphArrowR + " " + e.Target + " " + GlyphNone + " " + humanAge(max(0, in.Now.Sub(eventTimestamp(e.ID)))),
			Detail: e.Message, Kind: "thread", Agent: e.From, Ref: thread, Event: e, Selectable: true})
	}
	for _, n := range names {
		a := in.Agents[n]
		if a.state == "blocked" {
			add(recapRow{Key: "blocked:" + n, Section: recapForYou, Title: n + " blocked", Detail: a.message,
				Kind: "agent", Agent: n, Selectable: true})
		}
	}
	planRecapExpected(in, add)
	if err := in.Errors["Open asks"]; err != nil {
		add(recapRow{Section: recapForYou, Title: "Open asks unavailable", Detail: Preview(err.Error())})
	}
	if err := in.Errors["Agent snapshot"]; err != nil {
		add(recapRow{Section: recapForYou, Title: "Agent status unavailable", Detail: Preview(err.Error())})
	}
	planRecapSection(in, add, section)
	section("Agents")
	for _, n := range names {
		a := in.Agents[n]
		state := a.state
		if state == "" {
			state = "undeclared"
		}
		if answered(a) {
			state = "answered"
		}
		age := "age unknown"
		if !a.stateAt.IsZero() {
			age = humanAge(max(0, in.Now.Sub(a.stateAt)))
		}
		detail := a.message
		if a.held {
			detail = joinNote(detail, "quota held")
		}
		if a.paused {
			detail = joinNote(detail, "project paused")
		}
		add(recapRow{Key: "agent:" + n, Section: "Agents", Title: n + " " + GlyphNone + " " + state + " " + GlyphNone + " " + age,
			Detail: detail, Kind: "agent", Agent: n, Selectable: true})
	}
	if err := in.Errors["Quota"]; err != nil {
		add(recapRow{Section: "Agents", Title: "Quota state unavailable", Detail: Preview(err.Error())})
	}
	section("Board")
	tasks := make([]string, 0, len(in.Board))
	for task := range in.Board {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		a, b := in.Board[tasks[i]], in.Board[tasks[j]]
		if a.State != b.State {
			return a.State < b.State
		}
		return tasks[i] < tasks[j]
	})
	if len(tasks) == 0 {
		add(recapRow{Section: "Board", Title: "No board tasks"})
	}
	for _, task := range tasks {
		e := in.Board[task]
		add(recapRow{Key: "board:" + task, Section: "Board", Title: task + " " + GlyphNone + " " + e.State,
			Detail: "owner " + e.Owner + " " + GlyphNone + " " + e.Branch, Kind: "board", Task: task, Selectable: true})
	}
	if err := in.Errors["Board"]; err != nil {
		add(recapRow{Section: "Board", Title: "Board unavailable", Detail: Preview(err.Error())})
	}
	section("Since your last visit")
	if in.Since.IsZero() {
		add(recapRow{Section: "Since your last visit", Title: "No previous visit recorded; interval history is unavailable"})
	} else {
		add(recapRow{Section: "Since your last visit", Title: "Since " + in.Since.Local().Format("2006-01-02 15:04")})
	}
	for _, source := range []string{"cmd", "report", "verdict", "swap", "Services", "Pause"} {
		if err := in.Errors[source]; err != nil {
			add(recapRow{Section: "Since your last visit", Title: source + " unavailable", Detail: Preview(err.Error())})
		}
	}
	if !in.Since.IsZero() {
		for _, floor := range []struct {
			name string
			at   time.Time
		}{{"cmd", eventFloor(in.Commands)}, {"report", eventFloor(in.Reports)}, {"swap", swapFloor(in.Swaps)}, {"verdict", verdictFloor(in.Verdicts)}} {
			if in.Errors[floor.name] != nil {
				continue
			}
			if floor.at.IsZero() {
				m.Warnings = append(m.Warnings, floor.name+" has no retained entries; earlier history cannot be verified")
			} else if in.Since.Before(floor.at) {
				m.Warnings = append(m.Warnings, fmt.Sprintf("%s history before %s is not retained", floor.name, floor.at.Local().Format("2006-01-02 15:04")))
			}
		}
		m.Warnings = append(m.Warnings, "pause history is limited to the current record and retained resume directives")
		m.Warnings = append(m.Warnings, "service history is limited to each service's latest state")
		for _, e := range in.Commands {
			at := eventTimestamp(e.ID)
			if !at.After(in.Since) {
				continue
			}
			switch {
			case e.Type == bus.CmdComplete:
				add(recapRow{Key: "complete:" + e.ID, Section: "Since your last visit", Title: "Change completed " + GlyphNone + " " + e.Target,
					Detail: e.Message, Kind: "thread", Agent: e.From, Ref: threadID(e), Event: e, Selectable: true})
			case e.Type == bus.CmdDirective && strings.HasPrefix(e.Message, "Project resumed:"):
				add(recapRow{Key: "resume:" + e.ID, Section: "Since your last visit", Title: "Project resumed " + GlyphNone + " " + e.Target,
					Detail: e.Message, Kind: "thread", Agent: e.From, Ref: threadID(e), Event: e, Selectable: true})
			}
		}
		for i := range in.Verdicts {
			v := in.Verdicts[i]
			at := time.UnixMilli(v.TS)
			if !at.After(in.Since) {
				continue
			}
			copy := v
			add(recapRow{Key: "verdict:" + v.ID, Section: "Since your last visit", Title: strings.ToUpper(v.Decision) + " " + GlyphNone + " " + v.Subject,
				Detail: v.Reviewer + " reviewed " + v.Author + ": " + v.Message, Kind: "verdict", Ref: v.Ref, Verdict: &copy, Selectable: true})
		}
		for i := range in.Swaps {
			s := in.Swaps[i]
			at := eventTimestamp(s.ID)
			if !at.After(in.Since) {
				continue
			}
			copy := s
			what := "swap"
			if s.Kind == bus.SwapKindReload {
				what = "reload"
			}
			add(recapRow{Key: "swap:" + s.ID, Section: "Since your last visit", Title: s.Role + " " + GlyphNone + " " + what + " " + s.Event,
				Detail: swapDescription(s), Kind: "swap", Agent: s.Role, Swap: &copy, Selectable: true})
		}
		if in.Pause != nil && in.Pause.At > in.Since.UnixMilli() {
			add(recapRow{Key: "pause:" + fmt.Sprint(in.Pause.At), Section: "Since your last visit", Title: "Project paused by " + in.Pause.By,
				Detail: in.Pause.Reason, Kind: "pause", Selectable: true})
		}
		serviceNames := make([]string, 0, len(in.Services))
		for name := range in.Services {
			serviceNames = append(serviceNames, name)
		}
		sort.Strings(serviceNames)
		for _, name := range serviceNames {
			s := in.Services[name]
			if s.At > in.Since.UnixMilli() && (s.Error != "" || s.State == "failed") {
				add(recapRow{Key: "service:" + name, Section: "Since your last visit", Title: name + " service failure",
					Detail: firstNonempty(s.Error, s.Detail), Kind: "services", Selectable: true})
			}
		}
		counts := map[string]int{}
		for _, r := range in.Reports {
			if eventTimestamp(r.ID).After(in.Since) {
				counts[r.Agent]++
			}
		}
		reporters := make([]string, 0, len(counts))
		for n := range counts {
			reporters = append(reporters, n)
		}
		sort.Strings(reporters)
		for _, n := range reporters {
			add(recapRow{Key: "reports:" + n, Section: "Since your last visit", Title: fmt.Sprintf("%s %s %d reports", n, GlyphNone, counts[n]), Kind: "agent", Agent: n, Selectable: true})
		}
	}
	for _, warning := range m.Warnings {
		add(recapRow{Section: "Since your last visit", Title: warning})
	}
	for _, row := range healthRecapRows(in.Health) {
		row.Section = "Since your last visit"
		add(row)
	}
	if !in.Since.IsZero() && in.HealthFloor != "" && eventTimestamp(in.HealthFloor).After(in.Since) {
		add(recapRow{Section: "Since your last visit", Title: "health transition history before " + eventTimestamp(in.HealthFloor).Local().Format("2006-01-02 15:04") + " is not retained"})
	}
	if in.Errors["Health"] != nil {
		add(recapRow{Section: "Since your last visit", Title: "Health transitions unavailable"})
	}
	if in.Errors["cmd"] != nil {
		m.SummaryStatus = "request status unavailable"
	} else {
		for _, e := range in.Commands {
			if e.Human && e.Type == bus.CmdDirective && strings.HasPrefix(e.Message, "Summarise where the project stands for me: ") {
				at := eventTimestamp(e.ID)
				if at.After(m.SummarySentAt) {
					m.SummarySentAt = at
					m.SummaryReplied = false
					for _, reply := range in.Commands {
						if reply.Type == bus.CmdReply && reply.Ref == e.ID {
							m.SummaryReplied = true
							break
						}
					}
				}
			}
		}
		if m.SummarySentAt.IsZero() {
			m.SummaryStatus = "no request in retained history"
		}
	}
	if target := m.Target; target == "" {
		m.Target = "master"
	}
	if in.Pause != nil {
		m.SummaryDisabled = "project is paused"
	}
	if a := in.Agents[m.Target]; a == nil || !a.registered {
		if m.SummaryDisabled == "" {
			m.SummaryDisabled = "escalation target is not a registered agent"
		}
	} else if _, heldNow := held(in.Holds, a.provider, in.Now); heldNow {
		if m.SummaryDisabled == "" {
			m.SummaryDisabled = "escalation target's provider is held"
		}
	}
	m.Rows = append(m.Rows, recapRow{Key: "summary", Section: "Ask master", Title: "Ask " + m.Target + " for a summary",
		Detail: recapSummaryStatus(m), Kind: "summary", Selectable: m.SummaryDisabled == ""})
	return m
}

func recapTarget(cmds []bus.Event) string {
	var target string
	var latest time.Time
	for _, e := range cmds {
		if e.From == "dispatch" && e.Type == bus.CmdEscalation && bus.ValidName(e.Target) {
			at := eventTimestamp(e.ID)
			if at.After(latest) {
				latest, target = at, e.Target
			}
		}
	}
	return target
}

func recapSummaryStatus(m recapModel) string {
	if m.SummaryDisabled != "" {
		status := "disabled: " + m.SummaryDisabled
		if m.SummaryStatus != "" {
			status += " " + GlyphNone + " " + m.SummaryStatus
		}
		if !m.SummarySentAt.IsZero() {
			status += " " + GlyphNone + " last requested " + humanAge(max(0, m.Now.Sub(m.SummarySentAt))) + " ago"
			if m.SummaryReplied {
				status += " " + GlyphNone + " reply received"
			}
		}
		return status
	}
	if m.SummaryStatus != "" {
		return m.SummaryStatus
	}
	if m.SummarySentAt.IsZero() {
		return "not requested"
	}
	if m.SummaryReplied {
		return "last requested " + humanAge(max(0, m.Now.Sub(m.SummarySentAt))) + " ago " + GlyphNone + " reply received"
	}
	return "last requested " + humanAge(max(0, m.Now.Sub(m.SummarySentAt))) + " ago " + GlyphNone + " awaiting reply"
}

func eventFloor(events []bus.Event) time.Time {
	var floor time.Time
	for _, e := range events {
		if at := eventTimestamp(e.ID); !at.IsZero() && (floor.IsZero() || at.Before(floor)) {
			floor = at
		}
	}
	return floor
}

func swapFloor(events []bus.SwapEvent) time.Time {
	var floor time.Time
	for _, e := range events {
		if at := eventTimestamp(e.ID); !at.IsZero() && (floor.IsZero() || at.Before(floor)) {
			floor = at
		}
	}
	return floor
}

func verdictFloor(events []bus.Verdict) time.Time {
	var floor time.Time
	for _, e := range events {
		if e.TS > 0 {
			at := time.UnixMilli(e.TS)
			if floor.IsZero() || at.Before(floor) {
				floor = at
			}
		}
	}
	return floor
}

func swapDescription(s bus.SwapEvent) string {
	parts := []string{s.Event}
	if s.FromProfile != "" || s.ToProfile != "" {
		parts = append(parts, s.FromProfile+" "+GlyphArrowR+" "+s.ToProfile)
	}
	if s.Reason != "" {
		parts = append(parts, s.Reason)
	}
	if s.Error != "" {
		parts = append(parts, s.Error)
	}
	return strings.Join(parts, " "+GlyphNone+" ")
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
