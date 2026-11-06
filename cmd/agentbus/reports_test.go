package main

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

func TestReportsTable(t *testing.T) {
	evs := []bus.Event{
		{ID: "1-0", Agent: "claude2", RKind: "note", Message: "short one"},
		{ID: "2-0", Agent: "claude3", RKind: "auto", Message: "preview…", Full: "the\nfull\ntext"},
	}
	out := reportsTable(evs)
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(first, "1-0") || !strings.Contains(first, "claude2") || !strings.Contains(first, `"short one"`) {
		t.Fatalf("first row wrong: %q", first)
	}
	if strings.Contains(first, "(+") {
		t.Fatalf("short report must have no (+N) marker: %q", first)
	}
	if !strings.Contains(out, "(+13)") { // "the\nfull\ntext" = 13 runes
		t.Fatalf("full report must carry (+13): %q", out)
	}
}

func TestReportDetail(t *testing.T) {
	if got := reportDetail(bus.Event{TextComplete: "yes", Message: "preview…", Full: "the\nfull\ntext"}); got != "the\nfull\ntext" {
		t.Fatalf("detail should return Full: %q", got)
	}
	if got := reportDetail(bus.Event{TextComplete: "yes", Message: "just a preview"}); got != "just a preview" {
		t.Fatalf("detail should fall back to Message: %q", got)
	}
}

func TestReportDetailCompleteness(t *testing.T) {
	for _, tc := range []struct{ state, want string }{{"", "completeness unknown"}, {"no", "truncated at publication"}} {
		if got := reportDetail(bus.Event{Message: "text", TextComplete: tc.state}); !strings.Contains(got, tc.want) {
			t.Fatalf("%q lacks %s", got, tc.want)
		}
	}
}

func TestReportNoticeNamesTheReportAndItsFirstLine(t *testing.T) {
	got := reportNotice("1-0", "act4 APPROVED at 4d5d51b\nsecond line")
	if !strings.Contains(got, "agentbus reports 1-0") || !strings.Contains(got, "act4 APPROVED at 4d5d51b") || strings.Contains(got, "second line") {
		t.Fatalf("notice %q", got)
	}
	if n := len([]rune(reportNotice("1-0", strings.Repeat("x", 900)))); n > 400 {
		t.Fatalf("notice not bounded: %d runes", n)
	}
}

// report-one-row §4: the notice's subject is the report's headline, through
// the same guards (sanitised, 300 runes); the first line when it has none.
func TestReportNoticeCarriesTheHeadline(t *testing.T) {
	const pre = "Report 1-0 for you (full text: agentbus reports 1-0): "
	for _, c := range []struct{ msg, want string }{
		{"## Done\n- Vérif: all green\n- next", pre + "Vérif: all green"},
		{"## Done", pre + "Done"},
		{"| a | b |\n|---|", pre + "| a | b |"},              // no headline: today's first line
		{"## Done\nbell\x07here. Then", pre + "bell here."}, // a control character is a space
	} {
		if got := reportNotice("1-0", c.msg); got != c.want {
			t.Errorf("reportNotice(%q) = %q, want %q", c.msg, got, c.want)
		}
	}
	long := reportNotice("1-0", "## T\n"+strings.Repeat("y", 900))
	if sub := strings.TrimPrefix(long, pre); len([]rune(sub)) != 301 || !strings.HasSuffix(sub, "…") {
		t.Fatalf("headline not cut at 300 runes: %d", len([]rune(sub)))
	}
}

// §4 and the person's answer: the table shows the headline, read from the
// full text when the report keeps one.
func TestReportsTableShowsTheHeadline(t *testing.T) {
	out := reportsTable([]bus.Event{
		{ID: "1-0", Agent: "master", RKind: "note", Message: "## Done - Vérif: ok - next", Full: "## Done\n- Vérif: ok\n- next", TextComplete: "yes"},
		{ID: "2-0", Agent: "coder", RKind: "note", Message: "| only a table |", TextComplete: "yes"},
	})
	rows := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if !strings.Contains(rows[0], `"Vérif: ok"`) || strings.Contains(rows[0], "##") {
		t.Fatalf("row 1: %q", rows[0])
	}
	if !strings.Contains(rows[1], `"| only a table |"`) {
		t.Fatalf("row 2 keeps its preview with no headline: %q", rows[1])
	}
}
