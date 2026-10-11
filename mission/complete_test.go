package mission

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

func (h *harness) skipNow(id string) {
	h.t.Helper()
	h.setState(id, Blocked, nil)
	if ok, msg := h.op("skip", "chg/"+id, "by hand"); !ok {
		h.t.Fatalf("skip %s: %q", id, msg)
	}
}

func (h *harness) completes() []bus.Event {
	var out []bus.Event
	for _, e := range h.cmds() {
		if e.Type == bus.CmdComplete {
			out = append(out, e)
		}
	}
	return out
}

func (h *harness) escalationsAbout(word string) int {
	return countCmds(h.cmds(), func(e bus.Event) bool { return e.Type == bus.CmdEscalation && strings.Contains(e.Message, word) })
}

func TestCompleteSentOnce(t *testing.T) {
	h := newHarness(t, "true")
	h.integrateT1()
	h.pass()
	if len(h.completes()) != 0 {
		t.Fatal("complete sent while t2 is still open")
	}
	h.skipNow("t2")
	h.pass()
	cs := h.completes()
	if len(cs) != 1 || cs[0].Target != "master" ||
		!strings.Contains(cs[0].Message, "change chg rev 1 complete on change/chg@") ||
		!strings.Contains(cs[0].Message, "1 integrated, 1 skipped; open the PR to main") {
		t.Fatalf("completes = %+v", cs)
	}
	h.pass()
	restarted := *h.c // a conductor restart: same store, fresh process
	if err := restarted.Pass(h.ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(h.completes()); n != 1 {
		t.Fatalf("%d complete signals after more passes and a restart, want 1", n)
	}
	rec, _, _ := h.c.Store.Change(h.ctx, "chg")
	if rec.CompletedRev != 1 || rec.CompletedHead == "" || rec.State != "active" {
		t.Fatalf("record = %+v", rec)
	}
}

func TestCompleteAllSkipped(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	h.skipNow("t1")
	h.pass()
	h.skipNow("t2")
	h.pass()
	if cs := h.completes(); len(cs) != 1 || !strings.Contains(cs[0].Message, "0 integrated, 2 skipped") {
		t.Fatalf("completes = %+v", cs)
	}
}

func TestCompleteRedSuiteEscalatesOnce(t *testing.T) {
	h := newHarnessWith(t, "true", "false")
	h.pass()
	h.skipNow("t1")
	h.pass()
	h.skipNow("t2")
	h.pass()
	h.pass()
	if n := len(h.completes()); n != 0 {
		t.Fatalf("%d complete signals on a red suite", n)
	}
	if n := h.escalationsAbout("cannot complete"); n != 1 {
		t.Fatalf("%d escalations, want 1", n)
	}
}

func TestCompleteRearmsOnReload(t *testing.T) {
	h := newHarness(t, "true")
	h.pass()
	h.skipNow("t1")
	h.pass()
	h.skipNow("t2")
	h.pass()
	revised := strings.Replace(missionsFile("true"), `"title": "Second"`, `"title": "Second, revised"`, 1)
	if ok, msg := h.reload(revised, "retitle"); !ok {
		t.Fatalf("reload: %q", msg)
	}
	h.pass()
	cs := h.completes()
	if len(cs) != 2 || !strings.Contains(cs[1].Message, "rev 2 complete") {
		t.Fatalf("completes = %+v, want one per revision", cs)
	}
}
