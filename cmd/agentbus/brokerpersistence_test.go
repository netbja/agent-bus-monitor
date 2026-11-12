package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/health"
)

// The check reports a refused CONFIG as unknown without an error, and a
// broker it cannot read as unknown WITH its error, so the runner sees the
// failure (reviewer-codex).
func TestPersistenceOutcome(t *testing.T) {
	if s, d, err := persistenceOutcome("no", "", nil); s != health.Warn || err != nil || d == "" {
		t.Fatalf("neither: %s %q %v", s, d, err)
	}
	refused := fmt.Errorf("%w: NOPERM", bus.ErrConfigRefused)
	if s, d, err := persistenceOutcome("", "", refused); s != health.Unknown || err != nil || d != "the broker refused CONFIG GET" {
		t.Fatalf("refused: %s %q %v", s, d, err)
	}
	if s, d, err := persistenceOutcome("", "", context.DeadlineExceeded); s != health.Unknown || !errors.Is(err, context.DeadlineExceeded) || d != "broker persistence unavailable" {
		t.Fatalf("failed: %s %q %v", s, d, err)
	}
}
