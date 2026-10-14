package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

func TestServiceFact(t *testing.T) {
	now := time.Now()
	fresh, old := now.Add(-3*time.Second).UnixMilli(), now.Add(-time.Minute).UnixMilli()
	cases := []struct {
		name   string
		rec    bus.ServiceRecord
		has    bool
		legacy string
		want   string
	}{
		{"no record: the tab-mode facts", bus.ServiceRecord{}, false, "up", "up"},
		{"no record, nothing", bus.ServiceRecord{}, false, "", ""},
		{"ready and fresh", bus.ServiceRecord{State: "ready", At: fresh}, true, "up", "ready"},
		{"waiting for herdr", bus.ServiceRecord{State: "waiting-herdr", At: fresh}, true, "up", "waiting-herdr"},
		{"idle conductor", bus.ServiceRecord{State: "idle", At: fresh}, true, "up", "idle"},
		{"a running state gone stale: the process died", bus.ServiceRecord{State: "ready", At: old}, true, "down", "down"},
		{"stale record, a tab holds the lease", bus.ServiceRecord{State: "ready", At: old}, true, "up", "up"},
		{"failed stays visible", bus.ServiceRecord{State: "failed", At: old}, true, "down", "failed"},
		{"gave up stays visible", bus.ServiceRecord{State: "gave-up", At: old}, true, "down", "gave-up"},
		{"stopped by down", bus.ServiceRecord{State: "stopped", At: old}, true, "down", "stopped"},
		{"standby, the other instance is up", bus.ServiceRecord{State: "standby", At: fresh}, true, "up", "up"},
		{"standby, waiting to take over", bus.ServiceRecord{State: "standby", At: fresh}, true, "down", "standby"},
		{"standby in the overlay", bus.ServiceRecord{State: "standby", At: fresh}, true, "", "standby"},
		{"standby gone stale", bus.ServiceRecord{State: "standby", At: old}, true, "down", "down"},
		{"failed but another instance runs", bus.ServiceRecord{State: "failed", At: old}, true, "up", "up"},
	}
	for _, c := range cases {
		if got := serviceFact(c.rec, c.has, c.legacy, now); got != c.want {
			t.Errorf("%s: serviceFact = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestServiceSegWords(t *testing.T) {
	for state, want := range map[string]string{
		"up": "dispatch up", "down": "dispatch down", "ready": "dispatch ready", "waiting-herdr": "dispatch waiting herdr",
		"failed": "dispatch failed", "gave-up": "dispatch gave up", "stopped": "dispatch stopped", "starting": "dispatch starting",
	} {
		if got := lineText(serviceSeg("dispatch", "disp", state, false)); !strings.HasSuffix(got, want) {
			t.Errorf("%s: %q, want suffix %q", state, got, want)
		}
	}
	if got := lineText(serviceSeg("missions", "miss", "idle", true)); !strings.HasSuffix(got, "miss") {
		t.Errorf("compact = %q", got)
	}
	if r := serviceSeg("dispatch", "disp", "failed", false)[0].Role; r != RoleError {
		t.Errorf("failed role = %v", r)
	}
	if r := serviceSeg("dispatch", "disp", "waiting-herdr", false)[0].Role; r != RoleAttention {
		t.Errorf("waiting role = %v", r)
	}
}

func TestServicesPanelShowsStateErrorAndLocalLog(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "dispatch.log")
	os.WriteFile(log, []byte("old line\nservice: failed: broker went away\n"), 0o600)
	now := time.Now()
	inst := &bus.ServicesInstall{Kinds: []string{"dispatch", "missions"}, Manager: "systemd", Host: "here"}
	recs := map[string]bus.ServiceRecord{
		"dispatch": {State: "failed", Detail: "restarting", Error: "broker went away", At: now.Add(-5 * time.Second).UnixMilli(), PID: 7, Host: "here", Log: log},
		"missions": {State: "idle", Detail: "waiting for missions", At: now.UnixMilli(), Host: "elsewhere", Log: "/far/missions.log"},
	}
	body := servicesPanel(inst, recs, "here", now, 100)
	for _, want := range []string{"systemd", "dispatch", "failed", "restarting", "broker went away", "pid 7", log,
		"service: failed: broker went away", "missions", "idle", "waiting for missions", "on elsewhere", "agentbus --project"} {
		if !strings.Contains(body, want) {
			t.Fatalf("panel lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "/far/missions.log\n   old") {
		t.Fatal("a remote log was read")
	}
	sb := servicesPanel(inst, map[string]bus.ServiceRecord{"dispatch": {State: "standby", Detail: "another instance holds the lease", At: now.UnixMilli(), Host: "elsewhere"}}, "here", now, 100)
	if strings.Contains(sb, "process is gone") || !strings.Contains(sb, "standby") {
		t.Fatalf("a live standby shown as gone:\n%s", sb)
	}
	os.WriteFile(filepath.Join(dir, "dispatch.crash.log"), []byte("panic: boom\n"), 0o600)
	if crash := servicesPanel(inst, recs, "here", now, 100); !strings.Contains(crash, "panic: boom") {
		t.Fatalf("the crash file is not shown:\n%s", crash)
	}
	none := servicesPanel(nil, nil, "here", now, 100)
	if !strings.Contains(none, "herdr tabs") {
		t.Fatalf("no-services panel = %q", none)
	}
}

// The tick turns the service records into the header facts, and S opens the
// Services overlay from the model without reading the bus.
func TestPollShowsServiceStatesAndTheOverlayOpens(t *testing.T) {
	now := time.Now().UnixMilli()
	f := &fakeReader{calls: map[string]int{}, conductor: true,
		svcInst: &bus.ServicesInstall{Kinds: []string{"dispatch", "missions"}, Manager: "systemd", Host: "h"},
		svcRecs: map[string]bus.ServiceRecord{
			"dispatch": {State: "waiting-herdr", At: now, Error: "herdr: no server", Host: "h"},
			"missions": {State: "idle", Detail: "waiting for missions", At: now, Host: "h"},
		}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	pollOnce(context.Background(), f, st, 1)
	if st.dispatch != "waiting-herdr" || st.missions != "idle" {
		t.Fatalf("dispatch %q missions %q", st.dispatch, st.missions)
	}
	h := newHarness(t, st)
	top := strings.SplitN(h.draw(160, 45), "\n", 2)[0]
	if !strings.Contains(top, "dispatch waiting herdr") || !strings.Contains(top, "missions idle") {
		t.Fatalf("header = %q", top)
	}
	h.key(runeKey('s'))
	if screen := h.draw(160, 45); !strings.Contains(screen, "Services") || !strings.Contains(screen, "herdr: no server") {
		t.Fatalf("services overlay not shown:\n%s", screen)
	}
}

// --delete stops and removes the project's services before it deletes the
// keys; a removal that fails deletes nothing.
func TestDeleteRemovesTheServicesFirst(t *testing.T) {
	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	project := "del" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b, _ := bus.Open(c, project)
	ctx := context.Background()
	b.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}})
	t.Cleanup(func() { c.Del(ctx, bus.ServicesKey(project)) })

	if err := deleteProject("", project, true, strings.NewReader(""), time.Now(), func(string) ([]string, error) {
		return nil, errors.New("systemctl refused")
	}); err == nil || !strings.Contains(err.Error(), "nothing deleted") {
		t.Fatalf("err = %v", err)
	}
	if ok, _ := b.ServicesInstalled(ctx); !ok {
		t.Fatal("keys deleted although the services were not removed")
	}
	var keysAtRemoval int
	if err := deleteProject("", project, true, strings.NewReader(""), time.Now(), func(p string) ([]string, error) {
		keys, _ := bus.ProjectKeys(ctx, c, p)
		keysAtRemoval = len(keys)
		return []string{"dispatch"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if keysAtRemoval == 0 {
		t.Fatal("the services were removed after the keys")
	}
	if ok, _ := b.ServicesInstalled(ctx); ok {
		t.Fatal("the project survived --delete")
	}
}

// An agent launched and bound but with no status yet has TS 0: no 1970 age.
func TestPollKeepsAnAgentWithoutAStatusUndated(t *testing.T) {
	f := &fakeReader{calls: map[string]int{}, snaps: map[string]bus.AgentSnapshot{"coder": {Pane: "w2:p3", PID: 7}}}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}, health: health{ok: true}}
	pollOnce(context.Background(), f, st, 1)
	a := st.agents["coder"]
	if a == nil || !a.stateAt.IsZero() {
		t.Fatalf("agent = %+v, want an undated entry", a)
	}
}
