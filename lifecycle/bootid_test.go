package lifecycle

import (
	"testing"

	"github.com/netbja/agent-bus-monitor/dispatch"
)

// HostBootID is dispatch.BootID's id, or "" when it cannot be read
// (dispatchsvc P2, moved from cmd/agentbus main.go's hostBootID).
func TestHostBootID(t *testing.T) {
	want, err := dispatch.BootID()
	if err != nil {
		want = ""
	}
	if got := HostBootID(); got != want {
		t.Errorf("HostBootID() = %q, want %q", got, want)
	}
}
