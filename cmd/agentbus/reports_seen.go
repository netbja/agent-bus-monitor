package main

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// What `agentbus reports` says of each report's notices (report-one-row §5
// B): to a person, whether they opened it ({p}:seen, written by busmon); to
// an agent, whether its group delivered it.

// noticeState is one notice of a report.
type noticeState struct {
	Target    string
	ID        string
	Person    bool      // the target is no registered agent: its read state is {p}:seen
	SeenAt    time.Time // zero: not opened (or the receipt could not be read: Unknown)
	Unknown   bool      // the read state could not be read
	Delivered int       // an agent: 1 delivered, 0 pending, -1 unknown
}

// noticesByReport groups the report notices in cmds by report id: a directive
// whose text is reportNotice's for its own ref, nothing else.
func noticesByReport(cmds []bus.Event) map[string][]bus.Event {
	out := map[string][]bus.Event{}
	for _, e := range cmds {
		if e.Kind != "cmd" || e.Type != bus.CmdDirective || e.Ref == "" {
			continue
		}
		if !strings.HasPrefix(e.Message, "Report "+e.Ref+" for you (full text: agentbus reports "+e.Ref+"): ") {
			continue
		}
		out[e.Ref] = append(out[e.Ref], e)
	}
	return out
}

// noticeMark is one notice as the table says it.
func noticeMark(n noticeState) string {
	s := "→ " + n.Target
	switch {
	case n.Person && n.Unknown:
		return s + " ?"
	case n.Person && !n.SeenAt.IsZero():
		return s + " read " + n.SeenAt.Local().Format("15:04")
	case n.Person:
		return s + " unread"
	case n.Delivered == 1:
		return s + " delivered"
	case n.Delivered == 0:
		return s + " pending"
	}
	return s
}

// reportsTableWith is reportsTable with each report's notices marked.
func reportsTableWith(evs []bus.Event, states map[string][]noticeState) string {
	rows := strings.Split(strings.TrimSuffix(reportsTable(evs), "\n"), "\n")
	var sb strings.Builder
	for i, e := range evs {
		sb.WriteString(rows[i])
		if ns := states[e.ID]; len(ns) > 0 {
			marks := make([]string, len(ns))
			for j, n := range ns {
				marks[j] = noticeMark(n)
			}
			sb.WriteString("  " + strings.Join(marks, ", "))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// unreadReports are the reports with a notice to a person (to that target,
// when to is set) the person has not opened.
func unreadReports(evs []bus.Event, states map[string][]noticeState, to string) []bus.Event {
	var out []bus.Event
	for _, e := range evs {
		for _, n := range states[e.ID] {
			if n.Person && n.SeenAt.IsZero() && (to == "" || n.Target == to) {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

type noticeJSON struct {
	Target string  `json:"target"`
	ID     string  `json:"id"`
	Person bool    `json:"person"`
	SeenAt *string `json:"seen_at"`
}

type reportJSON struct {
	bus.Event
	Notices []noticeJSON `json:"notices,omitempty"`
}

// reportsJSON is reports --json: each report as before, plus its notices.
func reportsJSON(evs []bus.Event, states map[string][]noticeState) []byte {
	out := make([]reportJSON, 0, len(evs))
	for _, e := range evs {
		r := reportJSON{Event: e}
		for _, n := range states[e.ID] {
			j := noticeJSON{Target: n.Target, ID: n.ID, Person: n.Person}
			if !n.SeenAt.IsZero() {
				s := n.SeenAt.Format(time.RFC3339Nano)
				j.SeenAt = &s
			}
			r.Notices = append(r.Notices, j)
		}
		out = append(out, r)
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return b
}

// noticeReader is what readNoticeStates reads; *bus.Bus satisfies it.
type noticeReader interface {
	Recent(ctx context.Context, kinds []string, n int) ([]bus.Event, map[string]string, error)
	Agents(ctx context.Context) (map[string]bus.AgentSnapshot, error)
	Seen(ctx context.Context) (map[string]bus.Seen, error)
	Obligation(ctx context.Context, agent string, horizon time.Duration) (bus.Obligation, error)
}

// readNoticeStates reads every notice of evs from the retained cmd stream,
// with its read state. A failed read of {p}:seen is returned beside the
// states (marked Unknown), so the table says "?" and --unread refuses.
func readNoticeStates(ctx context.Context, r noticeReader, evs []bus.Event) (map[string][]noticeState, error, error) {
	cmds, _, err := r.Recent(ctx, []string{"cmd"}, 1000)
	if err != nil {
		return nil, nil, err
	}
	byReport := noticesByReport(cmds)
	agents, err := r.Agents(ctx)
	if err != nil {
		return nil, nil, err
	}
	seen, seenErr := r.Seen(ctx)
	delivered := map[string]string{} // agent → last-delivered id, "" unknown
	out := map[string][]noticeState{}
	for _, e := range evs {
		ns := byReport[e.ID]
		sort.Slice(ns, func(i, j int) bool { return bus.IDLess(ns[i].ID, ns[j].ID) })
		for _, n := range ns {
			st := noticeState{Target: n.Target, ID: n.ID, Delivered: -1}
			if _, isAgent := agents[n.Target]; !isAgent {
				st.Person, st.Unknown = true, seenErr != nil
				if s, ok := seen[n.ID]; ok {
					st.SeenAt = s.At
				}
			} else {
				last, ok := delivered[n.Target]
				if !ok {
					if o, err := r.Obligation(ctx, n.Target, bus.DefaultHorizon); err == nil && o.GroupExists {
						last = o.LastDelivered
					}
					delivered[n.Target] = last
				}
				if last != "" {
					st.Delivered = 0
					if !bus.IDLess(last, n.ID) {
						st.Delivered = 1
					}
				}
			}
			out[e.ID] = append(out[e.ID], st)
		}
	}
	return out, seenErr, nil
}
