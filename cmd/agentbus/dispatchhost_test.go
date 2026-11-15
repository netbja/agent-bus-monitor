package main

import (
	"context"
	"testing"

	"github.com/netbja/agent-bus-monitor/termhost"
	"github.com/netbja/agent-bus-monitor/termhost/fake"
)

// The dispatcher judges an agent with its live terminal's profile: the
// client the holder started it on; none for an agent without one.
func TestTermProfile(t *testing.T) {
	h := fake.New()
	ctx := context.Background()
	old, _ := h.Start(ctx, termhost.Spec{Project: "demo", Name: "coder", Profile: "codex"})
	if err := h.Close(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Start(ctx, termhost.Spec{Project: "demo", Name: "coder", Profile: "claude"}); err != nil {
		t.Fatal(err)
	}
	if got := termProfile(h, "demo", "coder"); got != "claude" {
		t.Fatalf("coder: %q, want its live terminal's", got)
	}
	if got := termProfile(h, "demo", "master"); got != "" {
		t.Fatalf("no terminal: %q", got)
	}
}
