package agentcaller

import "testing"

// Ancestor names "<project>/<agent>"; bus.ACLUserFor compares the agent
// ("master"), so AncestorOrNone hands it the agent alone.
func TestAgentOf(t *testing.T) {
	for in, want := range map[string]string{"demo/master": "master", "agent-bus-monitor/coder2": "coder2", "": "", "solo": "solo"} {
		if got := agentOf(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
