package bus

import "testing"

func TestPriorityRank(t *testing.T) {
	for level, want := range map[string]int{"high": 0, "": 1, "normal": 1, "bogus": 1, "low": 2} {
		if got := PriorityRank(level); got != want {
			t.Errorf("PriorityRank(%q) = %d, want %d", level, got, want)
		}
	}
}

func TestRequestLess(t *testing.T) {
	r := func(p string, at int64, id string) RequestInfo {
		return RequestInfo{Priority: p, CreatedAt: at, ID: id}
	}
	cases := []struct {
		name string
		a, b RequestInfo
		want bool
	}{
		{"high before normal", r("high", 9, "9-0"), r("", 1, "1-0"), true},
		{"normal before low", r("", 9, "9-0"), r("low", 1, "1-0"), true},
		{"high before low", r("high", 9, "9-0"), r("low", 1, "1-0"), true},
		{"low after high", r("low", 1, "1-0"), r("high", 9, "9-0"), false},
		{"equal level: older first", r("high", 1, "9-0"), r("high", 2, "1-0"), true},
		{"equal level: newer second", r("high", 2, "1-0"), r("high", 1, "9-0"), false},
		{"equal time: id order", r("", 5, "5-0"), r("", 5, "5-1"), true},
		{"equal time: id order, reversed", r("", 5, "5-1"), r("", 5, "5-0"), false},
		{"unknown reads normal", r("bogus", 1, "1-0"), r("low", 1, "1-0"), true},
	}
	for _, c := range cases {
		if got := RequestLess(c.a, c.b); got != c.want {
			t.Errorf("%s: RequestLess = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNextRequest(t *testing.T) {
	req := func(target, state, avail, prio string, at int64) BoardEntry {
		return BoardEntry{Owner: target, State: state, Request: &RequestInfo{ID: "x", Target: target, CreatedAt: at,
			Availability: avail, Priority: prio}}
	}
	cases := []struct {
		name  string
		board map[string]BoardEntry
		task  string
		why   string
	}{
		{"nothing", map[string]BoardEntry{}, "", "nothing ready"},
		{"the oldest of equal levels", map[string]BoardEntry{
			"a": req("coder", "requested", "retained", "", 2), "b": req("coder", "requested", "retained", "", 1)}, "b", ""},
		{"high before older normal", map[string]BoardEntry{
			"a": req("coder", "requested", "retained", "high", 9), "b": req("coder", "requested", "retained", "", 1)}, "a", ""},
		{"accepted wins whatever its level", map[string]BoardEntry{
			"a": req("coder", "accepted", "retained", "low", 9), "b": req("coder", "requested", "retained", "high", 1)}, "a", "finish first"},
		{"accepted with its body gone still holds", map[string]BoardEntry{
			"a": req("coder", "accepted", "missing", "", 9), "b": req("coder", "requested", "retained", "high", 1)}, "a", "finish first"},
		{"blocked does not hold", map[string]BoardEntry{
			"a": req("coder", "blocked", "retained", "high", 1), "b": req("coder", "requested", "retained", "low", 9)}, "b", ""},
		{"missing and expired skipped", map[string]BoardEntry{
			"a": req("coder", "requested", "missing", "high", 1), "b": req("coder", "requested", "expired", "high", 2),
			"c": req("coder", "requested", "retained", "", 9)}, "c", ""},
		{"done in review: no barrier", map[string]BoardEntry{
			"a": req("coder", "done", "retained", "", 1), "b": req("coder", "requested", "retained", "high", 9)}, "b", ""},
		{"another agent's ignored", map[string]BoardEntry{
			"a": req("foureyes", "requested", "retained", "high", 1), "b": req("coder", "requested", "retained", "low", 9)}, "b", ""},
		{"an untracked board task ignored", map[string]BoardEntry{
			"a": {Owner: "coder", State: "working"}}, "", "nothing ready"},
	}
	for _, c := range cases {
		task, why := NextRequest(c.board, "coder")
		if task != c.task || why != c.why {
			t.Errorf("%s: NextRequest = %q, %q, want %q, %q", c.name, task, why, c.task, c.why)
		}
	}
}
