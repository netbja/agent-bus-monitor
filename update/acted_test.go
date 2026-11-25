package update

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// audit step 2b: Run tells Acted once how an update it was asked to install
// ended, with the projects whose services run on this host (they run the
// replaced binaries); --check and up to date are no act.
func TestUpdateActed(t *testing.T) {
	run := func(arrange func(f *flow, d *Deps), o Options) []Act {
		f := newFlow(t)
		d := f.deps(false)
		var acts []Act
		d.Acted = func(a Act) { acts = append(acts, a) }
		if arrange != nil {
			arrange(f, &d)
		}
		Run(context.Background(), d, o)
		return acts
	}
	ok := run(nil, Options{Project: "a", Yes: true})
	if len(ok) != 1 || ok[0].Outcome != "ok" || ok[0].From != "v0.1.0" || ok[0].To != "v0.2.0" ||
		!reflect.DeepEqual(ok[0].Projects, []string{"a", "b"}) || !strings.HasPrefix(ok[0].Detail, "v0.1.0 -> v0.2.0, installed, snapshot ") {
		t.Fatalf("installed: %+v", ok)
	}
	red := run(func(f *flow, _ *Deps) { f.r.neverOK = "a missions" }, Options{Project: "a", Yes: true})
	if len(red) != 1 || red[0].Outcome != "failed" || red[0].Detail != "v0.1.0 -> v0.2.0, rolled back" {
		t.Fatalf("rolled back: %+v", red)
	}
	no := run(func(_ *flow, d *Deps) { d.TTY = &rw{in: strings.NewReader("n\n")} }, Options{})
	if len(no) != 1 || no[0].Outcome != "refused" || no[0].Detail != "v0.1.0 -> v0.2.0, not confirmed" {
		t.Fatalf("the person's no: %+v", no)
	}
	if got := run(nil, Options{Check: true}); len(got) != 0 {
		t.Fatalf("--check: %+v", got)
	}
	if got := run(func(_ *flow, d *Deps) { d.Version = "v0.2.0" }, Options{}); len(got) != 0 {
		t.Fatalf("up to date: %+v", got)
	}
}
