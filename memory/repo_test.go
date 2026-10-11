package memory_test

import (
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/memory"
)

// TestRepositoryPasses keeps this repository's own memory honest: an INDEX
// edit without `agentbus mem sync`, a broken link or a mislabelled status
// fails `go test ./...`, not only the next sentinel run.
func TestRepositoryPasses(t *testing.T) {
	r := memory.Check(memory.Options{Root: "..", Today: time.Now()})
	for _, w := range r.Warnings {
		t.Log("WARN:", w)
	}
	for _, e := range r.Errors {
		t.Error("FAIL:", e)
	}
}
