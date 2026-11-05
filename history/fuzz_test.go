package history

// Fuzz targets (plain `go test` runs only the seeds below). Fuzz one with
//
//	go test ./history -run '^$' -fuzz='^FuzzFilterMatch$' -fuzztime=20s -fuzzminimizetime=0
//
// -fuzzminimizetime=0: the default 60 s minimization of each new interesting
// input stalls the run at 0 execs/s for most of a short -fuzztime.

import (
	"testing"
)

// FuzzFilterMatch: Match never panics, the zero filter passes everything, and
// a filter built from an entry's own agent, thread, kind, text or time passes
// that entry.
func FuzzFilterMatch(f *testing.F) {
	f.Add("cmd", "1758141663000-0", "master", "coder", "directive", "", "do the thing")
	f.Add("report", "1758141663000-1", "", "", "", "1758141663000-0", "Done")
	f.Add("gap", "1758141663000-2", "", "", "", "", "")
	f.Add("verdicts", "bogus", "", "", "", "", "approve")
	f.Add("cmd", "", "", "", "verdict", "x", "")
	f.Fuzz(func(t *testing.T, stream, id, from, target, typ, ref, msg string) {
		e := Entry{Stream: stream, ID: id, F: map[string]string{
			"from": from, "target": target, "type": typ, "ref": ref, "message": msg,
			"of": target, "to": ref,
		}}
		if !(Filter{}).Match(e) {
			t.Fatalf("the zero filter refused %#v", e)
		}
		_ = (Filter{Agent: from, Thread: ref, Kind: typ, Grep: msg, Since: At(e), Until: At(e)}).Match(e)
		if stream == GapStream {
			if ref != "" && (Filter{Thread: ref}).Match(e) {
				t.Fatalf("a gap passed a thread filter")
			}
			return
		}
		checks := []Filter{
			{Agent: from},
			{Agent: target},
			{Thread: id},
			{Thread: ref},
			{Kind: Kind(e)},
			{Grep: msg},
			{Since: At(e), Until: At(e)},
		}
		for _, flt := range checks {
			if !flt.Match(e) {
				t.Fatalf("%+v refused its own entry %#v", flt, e)
			}
		}
	})
}
