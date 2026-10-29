package rolecfg

import (
	"strings"
	"testing"
)

// installer-no-client: one table of the clients agent-bus can launch, the
// installer's scan and up's guidance, in step with the launcher's own
// binaries, each with where to install it from (no network: a pointer only).
func TestClientsMatchTheLauncher(t *testing.T) {
	if len(Clients) != len(builtinBinary) {
		t.Fatalf("%d clients listed, the launcher knows %d", len(Clients), len(builtinBinary))
	}
	for _, c := range Clients {
		if builtinBinary[c.Client] != c.Exe {
			t.Errorf("%s: listed %q, the launcher runs %q", c.Client, c.Exe, builtinBinary[c.Client])
		}
		if c.Name == "" || !strings.HasPrefix(c.Install, "https://") {
			t.Errorf("%s: name %q, install %q", c.Client, c.Name, c.Install)
		}
	}
}

func TestClientGuidance(t *testing.T) {
	lines := ClientGuidance()
	if len(lines) != len(Clients) {
		t.Fatalf("%d lines for %d clients", len(lines), len(Clients))
	}
	for i, c := range Clients {
		if !strings.Contains(lines[i], c.Name) || !strings.Contains(lines[i], "("+c.Exe+")") || !strings.Contains(lines[i], c.Install) {
			t.Errorf("line %q lacks %s, (%s) or %s", lines[i], c.Name, c.Exe, c.Install)
		}
	}
}
