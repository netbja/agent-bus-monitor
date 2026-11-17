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
	d, hm = newDeps(live{n: 0, answered: false})
	if code, _, _ := stop(d, old); code != 0 || !hm.saw("stop-holder demo "+service.BuildKey(old)) {
		t.Fatalf("a holder that no longer answers: code %d calls %v", code, hm.calls)
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
