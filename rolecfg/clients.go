package rolecfg

import "fmt"

// ClientInfo is a client agent-bus can launch (installer-no-client): its
// manifest name, the executable the launcher runs, the name people know it by
// and where its maker says to install it from. agent-bus never installs one.
type ClientInfo struct {
	Client, Exe, Name, Install string
}

// Clients are the builtin clients, in the order the installer lists them;
// TestClientsMatchTheLauncher keeps them in step with builtinBinary.
var Clients = []ClientInfo{
	{Client: "claude-code", Exe: "claude", Name: "Claude Code", Install: "https://code.claude.com/docs/en/setup"},
	{Client: "codex", Exe: "codex", Name: "Codex CLI", Install: "https://github.com/openai/codex"},
	{Client: "kimi-cli", Exe: "kimi", Name: "Kimi Code CLI", Install: "https://github.com/MoonshotAI/kimi-cli"},
	{Client: "grok", Exe: "grok", Name: "Grok Build (xAI, beta)", Install: "https://x.ai/cli"},
}

// ClientGuidance is one line per client: its name, executable and install
// pointer.
func ClientGuidance() []string {
	out := make([]string, 0, len(Clients))
	for _, c := range Clients {
		out = append(out, fmt.Sprintf("%s (%s): %s", c.Name, c.Exe, c.Install))
	}
	return out
}
