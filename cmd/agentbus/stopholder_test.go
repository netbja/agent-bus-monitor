package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/service"
)

// agent-screen-blocked §4 (the person's Q5): one older build's holder is
// stopped by name, never one that holds a live terminal or answers without
// its list, never the current build's; a holder that no longer answers may be.
func TestServiceStopHolder(t *testing.T) {
	cur, old := "v0.29.0", "v0.28.0-886"
	type live struct {
		n        int
		answered bool
		err      error
	}
	newDeps := func(l live) (serviceCmdDeps, *fakeHolderManager) {
		hm := &fakeHolderManager{fakeManager: newFakeManager(), holders: map[string]service.HolderSpec{},
			active: map[string]bool{service.BuildKey(cur): true, service.BuildKey(old): true},
			units:  []service.HolderUnit{{Project: "demo", Key: service.BuildKey(cur), Raw: cur}, {Project: "demo", Key: service.BuildKey(old), Raw: old}}}
		return serviceCmdDeps{Manager: hm, HolderBuild: func() string { return cur },
			HolderLive: func(project, key string) (int, bool, error) {
				if project != "demo" || key != service.BuildKey(old) {
					t.Errorf("counted %s %s", project, key)
				}
				return l.n, l.answered, l.err
			}}, hm
	}
	stop := func(d serviceCmdDeps, args ...string) (int, string, string) {
		var out, errw bytes.Buffer
		code := runServiceCmd("demo", append([]string{"stop", "holder"}, args...), d, &out, &errw)
		return code, out.String(), errw.String()
	}

	d, hm := newDeps(live{n: 0, answered: true})
	if code, out, errw := stop(d, old); code != 0 || !hm.saw("stop-holder demo "+service.BuildKey(old)) || !strings.Contains(out, "holder (build "+old+"): stopped") || !strings.Contains(out, "service gc") {
		t.Fatalf("an empty older holder: code %d out %q err %q calls %v", code, out, errw, hm.calls)
	}
	d, hm = newDeps(live{n: 0, answered: true})
	if code, _, _ := stop(d, service.BuildKey(old)); code != 0 || !hm.saw("stop-holder demo "+service.BuildKey(old)) {
		t.Fatalf("by its key: code %d calls %v", code, hm.calls)
	}
	// reviewer-codex: a holder whose unit runs but whose socket does not
	// answer may still hold agents: refused, never stopped.
	d, hm = newDeps(live{n: 0, answered: false})
	if code, _, errw := stop(d, old); code == 0 || !strings.Contains(errw, "runs but does not answer") || strings.Contains(strings.Join(hm.calls, " "), "stop-holder") {
		t.Fatalf("an active holder that does not answer: code %d err %q calls %v", code, errw, hm.calls)
	}
	// foureyes: a unit state that cannot be read is refused, never read as
	// "not running".
	d, hm = newDeps(live{n: 0, answered: false})
	hm.activeErr = errors.New("systemctl: bus unreachable")
	if code, out, errw := stop(d, old); code != 1 || !strings.Contains(errw, "cannot be read") || strings.Contains(out, "not running") || strings.Contains(strings.Join(hm.calls, " "), "stop-holder") {
		t.Fatalf("unreadable state: code %d out %q err %q calls %v", code, out, errw, hm.calls)
	}
	// The refusal of a running holder that does not answer names what the
	// person can still do.
	d, _ = newDeps(live{n: 0, answered: false})
	if _, _, errw := stop(d, old); !strings.Contains(errw, "agentbus down demo") {
		t.Fatalf("refusal names no way out: %q", errw)
	}
	// Its unit not running: nothing to stop, gc removes it.
	d, hm = newDeps(live{n: 0, answered: false})
	hm.active[service.BuildKey(old)] = false
	if code, out, _ := stop(d, old); code != 0 || !strings.Contains(out, "not running") || !strings.Contains(out, "service gc") || strings.Contains(strings.Join(hm.calls, " "), "stop-holder") {
		t.Fatalf("an inactive holder: code %d out %q calls %v", code, out, hm.calls)
	}
	for name, c := range map[string]struct {
		l    live
		args []string
		want string
	}{
		"live terminals": {live{n: 2, answered: true}, []string{old}, "hosts 2 live terminal"},
		"list failed":    {live{answered: true, err: errors.New("list: broken pipe")}, []string{old}, "could not be listed"},
		"current build":  {live{}, []string{cur}, "the current build"},
		"unknown build":  {live{}, []string{"v0.1.0"}, "no holder unit of build v0.1.0"},
		"no build named": {live{}, nil, "usage"},
		"two builds":     {live{}, []string{old, cur}, "usage"},
	} {
		d, hm := newDeps(c.l)
		code, _, errw := stop(d, c.args...)
		if code == 0 || !strings.Contains(errw, c.want) || strings.Contains(strings.Join(hm.calls, " "), "stop-holder") {
			t.Errorf("%s: code %d err %q calls %v", name, code, errw, hm.calls)
		}
	}
}

// foureyes: the real deps carry the live-terminal count, so service stop
// holder is not refused everywhere by a missing hook.
func TestRealServiceCmdDepsCountLiveTerminals(t *testing.T) {
	d := realServiceCmdDeps("h1")
	if d.HolderLive == nil || d.HolderTerms == nil || d.HolderBuild == nil {
		t.Fatalf("deps lack a holder hook: live %v, terms %v, build %v", d.HolderLive != nil, d.HolderTerms != nil, d.HolderBuild != nil)
	}
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if n, answered, err := d.HolderLive("demo", "nokey"); n != 0 || answered || err != nil {
		t.Fatalf("no holder: %d %v %v", n, answered, err)
	}
}
