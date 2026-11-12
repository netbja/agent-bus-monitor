package bus

import (
	"context"
	"testing"
)

// A launcher that chose its client's session (Grok's --session-id, self-repair
// step 5) binds it with the process: the session is proved with that pid.
func TestBindRecordsTheLaunchSessionRedis(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	const id = "1a60c66b-d4e8-4a0e-8017-d50248645b38"
	if err := b.Bind(ctx, "grokker", AgentIdent{Pane: "w1:p2", PID: 42, PIDStart: 7, Client: "grok", Session: id}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	m, err := b.Agents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s := m["grokker"]; s.IdentSession != id || s.PID != 42 {
		t.Fatalf("snapshot = %+v, want session %s with pid 42", s, id)
	}
	// A new process with no session proves none: the old one is not kept.
	if err := b.Bind(ctx, "grokker", AgentIdent{Pane: "w1:p2", PID: 43, PIDStart: 9, Client: "grok"}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if m, _ = b.Agents(ctx); m["grokker"].IdentSession != "" {
		t.Fatalf("a new process kept session %q", m["grokker"].IdentSession)
	}
}

func TestBindRefusesASessionThatIsNoID(t *testing.T) {
	b := &Bus{}
	for _, s := range []string{"../x", "--resume", " s"} {
		if err := b.Bind(context.Background(), "grokker", AgentIdent{Pane: "w1:p2", PID: 42, PIDStart: 7, Session: s}); err == nil {
			t.Errorf("session %q accepted", s)
		}
	}
}
