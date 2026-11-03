package notify

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
)

// Decide is one pass of the notifier, pure (spec "Where it runs", "Dedup,
// delay and quiet", "The message"): from the events that hold now, the
// {p}:notified record and the listed targets, what each target is sent and
// what is recorded or pruned. The caller has read every source: a pass with
// a failed read never reaches Decide.

// Event is one fact the bus marks as the person's.
type Event struct {
	Kind    string    // decision, stall, fail, blocked, prompt, for-you
	Episode string    // unique within its kind; a new episode is sent again
	At      time.Time // since when it holds (its own time, else first seen)
	Line    string    // the summary line: who, what, the busmon key; never an agent's text
	Detail  string    // an agent's text, for full detail to a private target only
}

// Key is the event's part of a record field: <kind>|<episode>.
func (e Event) Key() string { return e.Kind + "|" + e.Episode }

// Settings are the notify.* keys.
type Settings struct {
	Events      []string
	Detail      string // summary or full
	Delay       time.Duration
	MaxPerHour  int
	Quiet       *config.Quiet // nil = no quiet hours
	QuietExempt []string
}

// SettingsFrom reads the notify.* keys.
func SettingsFrom(r config.Resolver) Settings {
	s := Settings{
		Events:      config.SplitList(r.String("notify.events")),
		Detail:      r.String("notify.detail"),
		Delay:       r.Duration("notify.delay"),
		MaxPerHour:  r.Int("notify.max_per_hour"),
		QuietExempt: config.SplitList(r.String("notify.quiet_exempt")),
	}
	if q, ok := config.ParseQuiet(r.String("notify.quiet")); ok {
		s.Quiet = &q
	}
	return s
}

// Pass is what one pass knows.
type Pass struct {
	Project    string
	Now        time.Time
	Events     []Event           // every event that holds now, whatever notify.events says
	Paused     bool              // {p}:pause holds: only a fail is sent
	Targets    []Target          // listed in notify.targets and valid
	Records    map[string]string // {p}:notified
	SentInHour func(target string) int
}

// TargetPlan is one target's part of a pass.
type TargetPlan struct {
	Target       Target
	TakeBaseline bool     // no _target|<name> yet: record Baseline, send nothing
	Baseline     []string // fields recorded as baseline
	Message      *Message // nil = nothing to send
	Fields       []string // written once the target answers with success
	Counted      bool     // the send counts in the hour's limit
}

// Plan is a pass's decision.
type Plan struct {
	Targets []TargetPlan
	Delete  []string // fields whose fact or target is gone
}

// limitExempt are the kinds the hourly limit never holds back (spec).
var limitExempt = map[string]bool{"fail": true, "stall": true}

const maxLines = 10

// TargetField is the record field for a target's baseline.
func TargetField(target string) string { return "_target|" + target }

func Decide(in Pass, s Settings) Plan {
	var p Plan
	holds := map[string]bool{}
	for _, e := range in.Events {
		holds[e.Key()] = true
	}
	listed := map[string]bool{}
	for _, t := range in.Targets {
		listed[t.Name] = true
	}
	for f := range in.Records {
		if t, ok := strings.CutPrefix(f, "_target|"); ok {
			if !listed[t] {
				p.Delete = append(p.Delete, f)
			}
			continue
		}
		i := strings.LastIndex(f, "|")
		if i < 0 || !listed[f[i+1:]] || !holds[f[:i]] {
			p.Delete = append(p.Delete, f)
		}
	}
	sort.Strings(p.Delete)

	quietNow := s.Quiet != nil && s.Quiet.Holds(in.Now)
	var due []Event
	for _, e := range in.Events {
		switch {
		case !slices.Contains(s.Events, e.Kind),
			in.Now.Sub(e.At) < s.Delay,
			in.Paused && e.Kind != "fail",
			quietNow && !slices.Contains(s.QuietExempt, e.Kind):
			continue
		}
		due = append(due, e)
	}
	sort.SliceStable(due, func(i, j int) bool {
		if !due[i].At.Equal(due[j].At) {
			return due[i].At.Before(due[j].At)
		}
		return due[i].Key() < due[j].Key()
	})

	for _, t := range in.Targets {
		tp := TargetPlan{Target: t}
		if _, ok := in.Records[TargetField(t.Name)]; !ok {
			tp.TakeBaseline = true
			for _, e := range in.Events {
				tp.Baseline = append(tp.Baseline, e.Key()+"|"+t.Name)
			}
			p.Targets = append(p.Targets, tp)
			continue
		}
		var send []Event
		for _, e := range due {
			if _, sent := in.Records[e.Key()+"|"+t.Name]; !sent {
				send = append(send, e)
			}
		}
		full := in.SentInHour != nil && s.MaxPerHour > 0 && in.SentInHour(t.Name) >= s.MaxPerHour
		if full {
			send = slices.DeleteFunc(send, func(e Event) bool { return !limitExempt[e.Kind] })
		}
		if len(send) > 0 {
			tp.Message = message(in, s, t, send)
			for _, e := range send {
				tp.Fields = append(tp.Fields, e.Key()+"|"+t.Name)
				tp.Counted = tp.Counted || !limitExempt[e.Kind]
			}
		}
		p.Targets = append(p.Targets, tp)
	}
	return p
}

// message is one target's message for events.
func message(in Pass, s Settings, t Target, events []Event) *Message {
	m := &Message{Project: in.Project, Title: fmt.Sprintf("%s · %d waiting for you", in.Project, len(events)), Priority: Low}
	full := s.Detail == "full" && t.Private()
	for i, e := range events {
		switch {
		case limitExempt[e.Kind]:
			m.Priority = High
		case e.Kind != "for-you" && m.Priority == Low:
			m.Priority = Default
		}
		if i == maxLines {
			m.Lines = append(m.Lines, fmt.Sprintf("and %d more: open busmon", len(events)-maxLines))
			continue
		}
		if i > maxLines {
			continue
		}
		line := e.Line
		if full && e.Detail != "" {
			line += ": " + DetailText(e.Detail)
		}
		if s.Quiet != nil && !s.Quiet.Holds(in.Now) && s.Quiet.Holds(e.At.Add(s.Delay)) {
			line += " (during quiet hours)"
		}
		m.Lines = append(m.Lines, line)
	}
	return m
}
