package main

import (
	"context"
	"errors"
	"os"
	"testing"
)

// TestMain stubs the guided setup's agent check, which reads the broker's
// agents: no test may reach the production broker (CLAUDE.md). A test that
// needs another answer replaces it and puts this one back.
func TestMain(m *testing.M) {
	nsAgentCaller = func(context.Context) (string, error) { return "", errors.New("no broker in tests") }
	os.Exit(m.Run())
}

// stubAgentCaller answers the agent check with caller and err for one test.
func stubAgentCaller(t *testing.T, caller string, err error) {
	t.Helper()
	old := nsAgentCaller
	nsAgentCaller = func(context.Context) (string, error) { return caller, err }
	t.Cleanup(func() { nsAgentCaller = old })
}
