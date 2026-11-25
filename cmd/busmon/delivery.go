package main

import (
	"context"
	"maps"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

// One approval for a small change in busmon (spec
// docs/superpowers/specs/2026-11-21-single-approval-design.md §4.5, §4.8;
// build step 3b): the project's delivery rows on Shift+C, and the warning
// when no reviewer of another provider reviewed in 7 days.

// providerWindow is the warning's span of the ledger (§4.8).
const providerWindow = 7 * 24 * 60 * 60 * 1000 // ms

// providerWarning is §4.8's line over vs, the independent verdicts of the
// last 7 days: "" when one was by a reviewer of another provider, or when
// there is none. "Another provider" is deliver's own rule (deliversingle.go):
// both providers known, and different; an unknown one is never another.
// value is the project's delivery.same_provider_ok: off says what one
// approval needs, docs or small make the line information.
func providerWarning(vs []bus.Verdict, value string) string {
	n := 0
	for _, v := range vs {
		if v.Reviewer == v.Author {
			continue
		}
		if v.ReviewerProvider != "" && v.AuthorProvider != "" && v.ReviewerProvider != v.AuthorProvider {
			return ""
		}
		n++
	}
	switch {
	case n == 0:
		return ""
	case value == "" || value == "off":
		return "no reviewer of another provider in 7 days: one approval needs delivery.same_provider_ok (now off)"
	}
	return "no reviewer of another provider in 7 days (delivery.same_provider_ok is " + value + ")"
}

// oneApprovalTraces are deliver's one-approval traces in evs (oldest first),
// by head: "<subject>@<head> delivered on one approval: <line>" (or "with
// one approval", the person's yes), the line after the colon, first line
// only, the newest kept. Only a deliverer's report counts (master, docs:
// decision 2026-11-02): another agent can write the same words.
func oneApprovalTraces(evs []bus.Event) map[string]string {
	var out map[string]string
	for _, e := range evs {
		if e.Kind != "report" || e.Agent != "master" && e.Agent != "docs" {
			continue
		}
		msg, _, _ := strings.Cut(e.Message, "\n")
		for _, mark := range []string{" delivered on one approval: ", " delivered with one approval: "} {
			at, line, ok := strings.Cut(msg, mark)
			if !ok {
				continue
			}
			i := strings.LastIndexByte(at, '@')
			if i < 0 || !bus.ValidHead(at[i+1:]) {
				continue
			}
			if out == nil {
				out = map[string]string{}
			}
			out[at[i+1:]] = line
		}
	}
	return out
}

// deliveryEvery is how often the 7 days' ledger is read again.
const deliveryEvery = time.Minute

// deliveryVerdictsMax bounds the ledger read; deliveryReportScan, the
// reports scanned for a new merge item's trace.
const (
	deliveryVerdictsMax = 5000
	deliveryReportScan  = 300
)

// deliveryFacts are what readDelivery read: the warning (§4.8; "" none),
// and deliver's trace line by merge item head (§4.5). Traces is replaced,
// never changed in place: a snapshot holds it.
type deliveryFacts struct {
	Warning string
	Traces  map[string]string
	at      time.Time       // the last ledger read
	looked  map[string]bool // the heads whose trace was looked for
}

// verdictsSinceReader is the ledger's read by time (the bus has it).
type verdictsSinceReader interface {
	VerdictsSince(ctx context.Context, ms int64, count int64) ([]bus.Verdict, error)
}

// readDelivery refreshes st.delivery on the requests' cadence: the ledger
// since now minus 7 days, at most once a deliveryEvery (a failed read keeps
// the last warning); the reports once per merge item head not looked up yet
// (deliver traces before the item enters the queue).
func readDelivery(ctx context.Context, b busReader, st *shared, now time.Time) {
	st.mu.Lock()
	f := st.delivery
	var fresh []string
	for _, it := range st.mergeQ.Items {
		if !f.looked[it.Head] {
			fresh = append(fresh, it.Head)
		}
	}
	value := "off"
	if st.sameProviderOf != nil {
		value = st.sameProviderOf()
	}
	st.mu.Unlock()
	if vr, ok := b.(verdictsSinceReader); ok && now.Sub(f.at) >= deliveryEvery {
		if vs, err := vr.VerdictsSince(ctx, now.UnixMilli()-providerWindow, deliveryVerdictsMax); err == nil {
			f.Warning, f.at = providerWarning(vs, value), now
		}
	}
	if len(fresh) > 0 {
		if evs, _, err := b.Recent(ctx, []string{"report"}, deliveryReportScan); err == nil {
			traces, looked := maps.Clone(f.Traces), maps.Clone(f.looked)
			if traces == nil {
				traces = map[string]string{}
			}
			if looked == nil {
				looked = map[string]bool{}
			}
			found := oneApprovalTraces(evs)
			for _, h := range fresh {
				looked[h] = true
				if l, ok := found[h]; ok {
					traces[h] = l
				}
			}
			f.Traces, f.looked = traces, looked
		}
	}
	st.mu.Lock()
	st.delivery = f
	st.mu.Unlock()
}

// localSameProviderOf reads project's delivery.same_provider_ok from
// config.toml ([project.<p>] only, as deliver reads it); off when unset or
// unreadable.
func localSameProviderOf(project string, getenv func(string) string) func() string {
	return func() string {
		f := config.Load(config.Path(getenv), "config.toml")
		if v, ok := config.ProjectValue(f, nil, project, "delivery.same_provider_ok").Value.(string); ok {
			return v
		}
		return "off"
	}
}

// deliveryLines end a merge item's page (§4.5, §4.8): deliver's
// one-approval trace for head, and the provider warning; none without them.
func deliveryLines(d deliveryFacts, head string) []Line {
	var out []Line
	if l, ok := d.Traces[head]; ok {
		out = append(out, Line{S(RoleMuted, "Delivered on one approval: "), S(RoleText, Preview(l))})
	}
	if d.Warning != "" {
		out = append(out, Line{S(RoleAttention, Preview(d.Warning))})
	}
	if len(out) > 0 {
		out = append([]Line{{}}, out...)
	}
	return out
}

// deliveryHelps are the delivery rows' help, before where they are set.
var deliveryHelps = map[string]string{
	"delivery.single_approval":  "small: a change deliver calls small needs one approval, by a reviewer of another provider; off: two",
	"delivery.small_max_lines":  "a small change's added + deleted lines over the merge-base diff, at most",
	"delivery.small_max_files":  "a small change's changed paths, at most (a rename counts two)",
	"delivery.never_small":      "path globs added to deliver's never-small list (bus/**, setup/**, the gate itself...); it only narrows",
	"delivery.same_provider_ok": "which small change one approval by a reviewer of the author's provider delivers: docs (docs-only), small (any), off (none)",
}

// projectDeliveryRows resolves the delivery rows of groups for project, as
// deliver reads them (config.ProjectValue: [project.<p>.delivery], then
// [delivery] but for same_provider_ok, then the default): read-only (no page
// writes a project's table), never in D's reset, their help naming where
// they are set. Without a project the rows are left as resolved.
func projectDeliveryRows(groups []settingGroup, getenv func(string) string, project string) []settingGroup {
	if project == "" {
		return groups
	}
	f := config.Load(config.Path(getenv), "config.toml")
	singleOff := config.ProjectValue(f, nil, project, "delivery.single_approval").Value == "off"
	for gi := range groups {
		for ri := range groups[gi].Rows {
			r := &groups[gi].Rows[ri]
			help, ok := deliveryHelps[r.Key]
			if !ok {
				continue
			}
			res := config.ProjectValue(f, nil, project, r.Key)
			r.Value, r.Source, r.From, r.Set = settingValue(res.Key, res.Value), res.Source, fromDefault, false
			if res.Source != "default" {
				r.From, r.Set = fromFile, config.Format(res.Value) != config.Format(res.Key.Default)
			}
			r.Editable, r.PersonOnly, r.InFile, r.FileHeld = false, true, false, false
			where := "[project." + project + ".delivery] in config.toml"
			if r.Key != "delivery.same_provider_ok" {
				where += ", else [delivery]"
			}
			r.Note = help + "; per project: " + where + ", yours to edit"
			if r.Key == "delivery.same_provider_ok" && singleOff {
				r.Note += "; no effect: delivery.single_approval is off"
			}
		}
	}
	return groups
}
