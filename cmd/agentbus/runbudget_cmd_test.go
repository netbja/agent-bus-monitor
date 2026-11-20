package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// §3: request send from an agent (AGENT_BUS_AGENT set) is refused while the
// run drains; the person's (unset) goes through.
func TestRequestSendWhileDraining(t *testing.T) {
	b, _ := dialMain(t)
	w := struct {
		b   *bus.Bus
		ctx context.Context
	}{b, context.Background()}
	if ok, err := w.b.AcquireDispatch(w.ctx, "tok", time.Minute); err != nil || !ok {
		t.Fatalf("AcquireDispatch = %v, %v", ok, err)
	}
	if _, _, err := w.b.FencedRunPass(w.ctx, "tok", nil, true, 15*time.Minute); err != nil { // a running run, as a merge-queue pass leaves it
		t.Fatal(err)
	}
	if _, _, err := w.b.FencedRunDrain(w.ctx, "tok", bus.RunWhyDuration); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"AGENT_BUS_AGENT": "master"}
	old := requestGetenv
	requestGetenv = func(k string) string { return env[k] }
	t.Cleanup(func() { requestGetenv = old })
	var out strings.Builder
	err := runRequest(w.ctx, w.b, "master", []string{"send", "t1", "coder", "do", "it"}, &out)
	if err == nil || !strings.Contains(err.Error(), "run is draining") {
		t.Fatalf("an agent's send: %v", err)
	}
	delete(env, "AGENT_BUS_AGENT")
	if err := runRequest(w.ctx, w.b, "charlie", []string{"send", "t2", "coder", "do", "it"}, &out); err != nil {
		t.Fatalf("the person's send: %v", err)
	}
}
