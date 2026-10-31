package main

import (
	"path/filepath"
	"slices"
	"testing"
)

// stranger-test: up tells bootstrap it runs it, so bootstrap does not tell the person to run
// agentbus up, which they just did.
func TestUpTellsBootstrapItRunsIt(t *testing.T) {
	cmd := bootstrapCommand("/r", []string{"recall", "demo"})
	if cmd.Path != filepath.Join("/r", "scripts", "bootstrap") || !slices.Equal(cmd.Args[1:], []string{"recall", "demo"}) {
		t.Fatalf("command %s %v", cmd.Path, cmd.Args)
	}
	if !slices.Contains(cmd.Env, upCallerEnv) {
		t.Fatalf("bootstrap's environment lacks %s", upCallerEnv)
	}
	if len(cmd.Env) < 2 {
		t.Fatalf("bootstrap's environment is only %v: the rest of up's was dropped", cmd.Env)
	}
}
