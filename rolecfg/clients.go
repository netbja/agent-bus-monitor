package rolecfg

import "fmt"

// ClientInfo is a client agent-bus can launch (installer-no-client): its
// manifest name, the executable the launcher runs, the name people know it by
// and where its maker says to install it from. agent-bus never installs one.
//
// Resume is the client's own form resuming a session, and ResumeSource where
// the session id comes from (self-repair step 5, herdr-exit §4.9). Only a
// client whose source is verified resumes (ResumeVerified); every other one
// starts fresh, never with a flag that guesses (--continue takes the folder's
// latest session, which may be another agent's when roles share a folder).
type ClientInfo struct {
	Client, Exe, Name, Install string
	Resume, ResumeSource       string
	resumeVerified             bool
}

// Clients are the builtin clients, in the order the installer lists them;
// TestClientsMatchTheLauncher keeps them in step with builtinBinary.
var Clients = []ClientInfo{
	{Client: "claude-code", Exe: "claude", Name: "Claude Code", Install: "https://code.claude.com/docs/en/setup",
		Resume: "claude --resume <id>", ResumeSource: "the SessionStart hook, proved by its ancestors (roles report-resume)", resumeVerified: true},
	{Client: "codex", Exe: "codex", Name: "Codex CLI", Install: "https://github.com/openai/codex",
		Resume: "codex resume <id>", ResumeSource: "the SessionStart hook; not verified: its caller proof under the shared app-server needs a probe in a Codex pane the person approves (it re-trusts the launch hooks)"},
	{Client: "kimi-cli", Exe: "kimi", Name: "Kimi Code CLI", Install: "https://github.com/MoonshotAI/kimi-code", // the successor of the archived kimi-cli (reviewer-codex)
		Resume: "kimi --session <id>", ResumeSource: "none: Kimi hooks are not used (the person, 2026-11-02) and kimi session list is per folder, ambiguous when roles share one"},
	{Client: "grok", Exe: "grok", Name: "Grok Build (xAI, beta)", Install: "https://x.ai/cli",
		Resume: "grok --resume <id>", ResumeSource: "the launcher could choose it (--session-id; resume verified with grok 1.0.77, 2026-11-12), but /new and /clear start another session that only its SessionStart hook reports; not verified: the hook fires only from a hook file the person trusts"},
	{Client: "agy", Exe: "agy", Name: "Antigravity CLI (Google)", Install: "https://antigravity.google/docs/cli/reference", // the page agy's own bundled docs name
		Resume: "agy --conversation <id>", ResumeSource: "conversationId in its hook payloads; not verified: wiring hooks.json is a write the person approves"},
}

// ResumeVerified reports whether client's session id source is verified, so
// a launch may resume it.
func ResumeVerified(client string) bool {
	for _, c := range Clients {
		if c.Client == client {
			return c.resumeVerified
		}
	}
	return false
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
