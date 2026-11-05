package plan

// Fuzz targets (plain `go test` runs only the seeds below). Fuzz one with
//
//	go test ./plan -run '^$' -fuzz='^FuzzParse$' -fuzztime=20s -fuzzminimizetime=0
//
// -fuzzminimizetime=0: the default 60 s minimization of each new interesting
// input stalls the run at 0 execs/s for most of a short -fuzztime.

import (
	"encoding/json"
	"reflect"
	"testing"
)

// FuzzParse: Parse never panics; a plan it accepts validates, stays within
// MaxTasks, and survives a marshal/parse round trip unchanged.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		``,
		`{}`,
		`{"plan":"p","goal":"g","tasks":[{"id":"a","title":"A","owner":"coder"}]}`,
		`{"plan":"p","goal":"g","tasks":[{"id":"a","title":"A","owner":"person"},{"id":"b","title":"B","owner":"coder","board":"b","review":"pr:12","merge":false,"after":["a"]}]}`,
		`{"plan":"p","goal":"g","tasks":[{"id":"a","title":"A","owner":"coder","mission":"c/m1"}]}`,
		`{"plan":"p","goal":"g","tasks":[{"id":"a","title":"A","owner":"coder","after":["b"]},{"id":"b","title":"B","owner":"coder","after":["a"]}]}`,
		`{"plan":"p","goal":"g","tasks":[],"extra":1}`,
		`{"plan":"p"} {}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := Parse(data)
		if err != nil {
			return
		}
		if err := Validate(p); err != nil {
			t.Fatalf("Parse accepted a plan Validate refuses: %v", err)
		}
		if len(p.Tasks) == 0 || len(p.Tasks) > MaxTasks {
			t.Fatalf("accepted %d tasks", len(p.Tasks))
		}
		out, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		q, err := Parse(out)
		if err != nil {
			t.Fatalf("re-parse of %s: %v", out, err)
		}
		if !reflect.DeepEqual(normalize(p), normalize(q)) {
			t.Fatalf("round trip changed the plan:\n%#v\n%#v", p, q)
		}
	})
}

// normalize: an empty After and a missing one are the same plan.
func normalize(p File) File {
	ts := make([]Task, len(p.Tasks))
	for i, t := range p.Tasks {
		if len(t.After) == 0 {
			t.After = nil
		}
		ts[i] = t
	}
	p.Tasks = ts
	return p
}
