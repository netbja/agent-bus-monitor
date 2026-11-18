package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/service"
)

// The holders' rows merge the manager's units with the holders that answer:
// a unit that runs without answering says so, an answering holder without a
// unit (a detached supervisor) is still listed (agent-screen-blocked §4).
func TestHolderRowsMergeUnitsAndAnswers(t *testing.T) {
	now := time.Now()
	units := []service.HolderUnit{{Project: "demo", Key: "k886", Raw: "v886"}, {Project: "demo", Key: "k888", Raw: "v888"}, {Project: "demo", Key: "k877", Raw: "v877"}}
	active := map[string]bool{"k886": true, "k888": true}
	states := []holder.HolderState{{Build: "v888", Listed: true, Live: 6}, {Build: "v886", Listed: true}, {Build: "v900", Listed: false}}
	since := map[string]time.Time{"v888": now.Add(-3 * time.Hour)}
	rows := holderRows(units, active, states, since)
	got := map[string]holderRow{}
	for _, r := range rows {
		got[r.Raw] = r
	}
	if r := got["v888"]; !r.Unit || !r.Active || !r.Answers || !r.Listed || r.Live != 6 || !r.Since.Equal(since["v888"]) {
		t.Errorf("v888 %+v", r)
	}
	if r := got["v877"]; !r.Unit || r.Active || r.Answers {
		t.Errorf("v877 (stopped) %+v", r)
	}
	if r := got["v900"]; r.Unit || !r.Answers || r.Listed {
		t.Errorf("v900 (no unit, unlisted) %+v", r)
	}
	if len(rows) != 4 || rows[0].Raw != "v877" {
		t.Errorf("rows %+v (sorted by build)", rows)
	}
}

// The section: each holder's state, build, uptime and agents; an older
// build that hosts no agent is flagged with the stop command and gc; the
// dispatcher's own build missing is said; busmon only reads.
func TestServicesHoldersSection(t *testing.T) {
	now := time.Now()
	f := svcFacts{host: "here", recs: map[string]bus.ServiceRecord{"dispatch": {State: "holder-behind", Build: "v0.29.0", At: now.UnixMilli(), Host: "here"}},
		holders: []holderRow{
			{Raw: "v0.28.0-886", Unit: true, Active: true, Answers: true, Listed: true, Live: 0, Since: now.Add(-26 * time.Hour)},
			{Raw: "v0.28.0-888", Unit: true, Active: true, Answers: true, Listed: true, Live: 6, Since: now.Add(-3 * time.Hour)},
			{Raw: "v0.28.0-877", Unit: true},
			{Raw: "v0.28.0-880", Unit: true, Active: true},
		}, holdersRead: true}
	for _, width := range []int{80, 160} {
		got := plainOf(servicesCards(f, 0, now, width))
		for _, want := range []string{"holders", "v0.28.0-888", "running", "up 3h", "hosts 6 agents", "v0.28.0-886", "hosts no agent",
			"old build, hosts no agent", "service stop holder v0.28.0-886", "service gc", "v0.28.0-877", "inactive", "v0.28.0-880", "not answering",
			"no holder of the dispatcher's build (v0.29.0) answers"} {
			if !strings.Contains(got, want) {
				t.Errorf("width %d lacks %q:\n%s", width, want, got)
			}
		}
		if strings.Contains(got, "service stop holder v0.28.0-888") {
			t.Errorf("width %d: the holder with agents is flagged:\n%s", width, got)
		}
		for _, l := range servicesCards(f, 0, now, width) {
			if l.Width() > width {
				t.Errorf("width %d: a line is %d cells", width, l.Width())
			}
		}
	}
	// The dispatcher's own build answering: no missing line, and it is not
	// flagged old even with no agent.
	f.recs["dispatch"] = bus.ServiceRecord{State: "ready", Build: "v0.28.0-886", At: now.UnixMilli(), Host: "here"}
	got := plainOf(servicesCards(f, 0, now, 160))
	if strings.Contains(got, "no holder of the dispatcher's build") || strings.Contains(got, "service stop holder v0.28.0-886") {
		t.Errorf("current build:\n%s", got)
	}
	// Not read (another host, no manager): the section says so, no rows.
	if got := plainOf(servicesCards(svcFacts{holdersErr: "not on this host"}, 0, now, 100)); !strings.Contains(got, "holders not read: not on this host") {
		t.Errorf("not read:\n%s", got)
	}
}

// The page reads the holders on busmon's own host only: with the dispatcher
// on another machine it says where they are instead; a failed read says why.
func TestGatherServicesReadsTheHolders(t *testing.T) {
	r := &fakeReader{calls: map[string]int{}}
	rows := []holderRow{{Raw: "v888", Answers: true, Listed: true, Live: 2}}
	read := 0
	holders := func(context.Context) ([]holderRow, error) { read++; return rows, nil }
	here := map[string]bus.ServiceRecord{"dispatch": {Host: "here", State: "ready"}}
	if f := gatherServices(context.Background(), r, "p", nil, here, "here", nil, holders); !f.holdersRead || len(f.holders) != 1 || f.project != "p" {
		t.Fatalf("here: %+v", f)
	}
	far := map[string]bus.ServiceRecord{"dispatch": {Host: "vdr", State: "ready"}}
	if f := gatherServices(context.Background(), r, "p", nil, far, "here", nil, holders); f.holdersRead || !strings.Contains(f.holdersErr, "vdr") {
		t.Fatalf("another host: %+v", f)
	}
	if read != 1 {
		t.Fatalf("read %d times, want once (not for another host)", read)
	}
	broken := func(context.Context) ([]holderRow, error) { return nil, errors.New("no runtime dir") }
	if f := gatherServices(context.Background(), r, "p", nil, here, "here", nil, broken); f.holdersRead || f.holdersErr != "no runtime dir" {
		t.Fatalf("failed: %+v", f)
	}
}
