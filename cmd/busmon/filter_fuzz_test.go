package main

// Fuzz targets (plain `go test` runs only the seeds below). Fuzz one with
//
//	go test ./cmd/busmon -run '^$' -fuzz='^FuzzParseFilter$' -fuzztime=20s -fuzzminimizetime=0
//
// -fuzzminimizetime=0: the default 60 s minimization of each new interesting
// input stalls the run at 0 execs/s for most of a short -fuzztime.

import (
	"strings"
	"testing"
	"unicode"

	"github.com/netbja/agent-bus-monitor/bus"
)

// FuzzParseFilter: the Activity/History filter syntax never panics; its parts
// carry no space, the agent and text are lower case, describe is empty iff the
// filter is, and a line made of the filter's own agent, thread and words
// passes it.
func FuzzParseFilter(f *testing.F) {
	for _, s := range []string{"", "@coder rebase", "1758141663000-0", "1758141663000", "@ @x 123 words  here", "@CODER Mixed CASE", "12-34-56", "-1", "İstanbul", "K"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		fl := parseFilter(s)
		for _, part := range []string{fl.agent, fl.thread} {
			if strings.IndexFunc(part, unicode.IsSpace) >= 0 {
				t.Fatalf("part %q holds a space", part)
			}
		}
		if fl.thread != "" && !looksLikeStreamID(fl.thread) {
			t.Fatalf("thread %q is no stream id", fl.thread)
		}
		if (fl.describe() == "") != fl.empty() {
			t.Fatalf("describe %q vs empty %v", fl.describe(), fl.empty())
		}
		if parseFilter(fl.raw) != fl {
			t.Fatalf("re-parse of %q differs", fl.raw)
		}
		if !(feedFilter{}).match(feedLine{ev: bus.Event{Kind: "cmd", Message: s}}) {
			t.Fatal("the empty filter refused a line")
		}
		id := fl.thread
		if id == "" {
			id = "1-0"
		}
		own := feedLine{ev: bus.Event{Kind: "cmd", ID: id, Agent: fl.agent, Message: fl.text}}
		if !fl.match(own) {
			t.Fatalf("filter %q (%+v) refused its own line", s, fl)
		}
	})
}
