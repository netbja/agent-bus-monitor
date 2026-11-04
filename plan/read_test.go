package plan

import (
	"reflect"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestReviewSubjectPrefersTheReviewField(t *testing.T) {
	for _, tc := range []struct {
		name string
		task Task
		want string
	}{
		{"review field", Task{Review: "pr-review", Board: "impl"}, "pr-review"},
		{"board task", Task{Board: "impl"}, "impl"},
		{"neither", Task{}, ""},
	} {
		if got := tc.task.ReviewSubject(); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestBySubjectKeepsLedgerOrderAndFilters(t *testing.T) {
	vs := []bus.Verdict{
		{ID: "1-0", Subject: "a"},
		{ID: "2-0", Subject: "b"},
		{ID: "3-0", Subject: "a"},
		{ID: "4-0", Subject: "c"},
	}
	for _, tc := range []struct {
		name string
		keep map[string]bool
		want map[string][]string
	}{
		{"all", nil, map[string][]string{"a": {"1-0", "3-0"}, "b": {"2-0"}, "c": {"4-0"}}},
		{"kept only", map[string]bool{"a": true, "c": true}, map[string][]string{"a": {"1-0", "3-0"}, "c": {"4-0"}}},
		{"none kept", map[string]bool{}, map[string][]string{}},
	} {
		got := map[string][]string{}
		for s, l := range BySubject(vs, tc.keep) {
			for _, v := range l {
				got[s] = append(got[s], v.ID)
			}
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	if got := BySubject(nil, nil); len(got) != 0 {
		t.Errorf("no verdicts: got %v", got)
	}
}

func TestChangesAreDistinctInFileOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tasks []Task
		want  []string
	}{
		{"no mission", []Task{{ID: "a"}, {ID: "b"}}, nil},
		{"mission without slash", []Task{{ID: "a", Mission: "lonely"}}, nil},
		{"distinct, first seen first", []Task{
			{ID: "a", Mission: "v2/deploy"},
			{ID: "b", Mission: "v1/x"},
			{ID: "c", Mission: "v2/other"},
		}, []string{"v2", "v1"}},
	} {
		if got := Changes(File{Tasks: tc.tasks}); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAllSubjectsJoinsReviewAndPRSubjects(t *testing.T) {
	f := File{Tasks: []Task{
		{ID: "a", Board: "a-impl"},
		{ID: "b", Review: "b-review"},
		{ID: "c"},
	}}
	facts := map[string]Fact{"a": {PR: 12}, "c": {PR: 7}}
	want := map[string]bool{"a-impl": true, "pr:12": true, "b-review": true, "pr:7": true}
	if got := AllSubjects(f, facts); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := AllSubjects(File{}, nil); len(got) != 0 {
		t.Fatalf("empty plan: got %v", got)
	}
}

func TestLabel(t *testing.T) {
	for _, tc := range []struct {
		s    State
		want string
	}{
		{Waiting, "waiting for you"},
		{Answered, "answered, master to record"},
		{InReview, "in review"},
		{InProgress, "in progress"},
		{Done, "done"},
		{Blocked, "blocked"},
		{Upcoming, "upcoming"},
		{Unknown, "?"},
	} {
		if got := Label(tc.s); got != tc.want {
			t.Errorf("Label(%q) = %q, want %q", tc.s, got, tc.want)
		}
	}
}

func TestAge(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		ago  time.Duration
		want string
	}{
		{-time.Hour, "0s"}, // a time in the future reads as now
		{0, "0s"},
		{45 * time.Second, "45s"},
		{time.Minute, "1m"},
		{59 * time.Minute, "59m"},
		{time.Hour, "1h"},
		{47 * time.Hour, "47h"},
		{48 * time.Hour, "2d"},
		{10 * 24 * time.Hour, "10d"},
	} {
		if got := Age(now.Add(-tc.ago).UnixMilli(), now); got != tc.want {
			t.Errorf("Age(-%v) = %q, want %q", tc.ago, got, tc.want)
		}
	}
}

func TestDoneLabel(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	at := now.Add(-3 * time.Hour).UnixMilli()
	no := false
	for _, tc := range []struct {
		name string
		row  Row
		want string
	}{
		{"recorded merge", Row{Recorded: true, DoneAt: at, Task: Task{Owner: "coder"}}, "merged (recorded 3h ago)"},
		{"recorded, no merge", Row{Recorded: true, DoneAt: at, Task: Task{Owner: "coder", Merge: &no}}, "done (recorded 3h ago)"},
		{"recorded person task", Row{Recorded: true, DoneAt: at, Task: Task{Owner: Person}}, "done (recorded 3h ago)"},
		{"reason", Row{Reason: "approved, no merge"}, "approved, no merge"},
		{"plain", Row{}, "done"},
	} {
		if got := DoneLabel(tc.row, now); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestQuiet(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	old := now.Add(-QuietAfter - time.Minute).UnixMilli()
	recent := now.Add(-time.Hour).UnixMilli()
	for _, tc := range []struct {
		name string
		row  Row
		want bool
	}{
		{"in progress, old fact", Row{State: InProgress, LastFact: old}, true},
		{"in progress, recent fact", Row{State: InProgress, LastFact: recent}, false},
		{"in progress, no fact", Row{State: InProgress}, false},
		{"in review, old fact", Row{State: InReview, LastFact: old}, false},
	} {
		if got := Quiet(tc.row, now); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCount(t *testing.T) {
	for _, tc := range []struct {
		name        string
		rows        []Row
		done, total int
	}{
		{"empty", nil, 0, 0},
		{"mixed", []Row{{State: Done}, {State: InProgress}, {State: Done}, {State: Waiting}}, 2, 4},
		{"none done", []Row{{State: Upcoming}}, 0, 1},
	} {
		d, n := Count(tc.rows)
		if d != tc.done || n != tc.total {
			t.Errorf("%s: got %d/%d, want %d/%d", tc.name, d, n, tc.done, tc.total)
		}
	}
}
