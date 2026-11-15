package agentstate

// busClientProfiles maps an agent's bus client name (AGENT_BUS_CLIENT, the
// snapshot's client) to its profile's file name.
var busClientProfiles = map[string]string{"claude-code": "claude", "codex": "codex", "kimi-cli": "kimi", "grok": "grok", "agy": "agy"}

// LoginSourceOf is the login source declared by the profile of the bus
// client client ("" when none, or no profile): where its fixed login expiry
// is read (login-updates r9 §3.4).
func LoginSourceOf(client string) string {
	name, ok := busClientProfiles[client]
	if !ok {
		return ""
	}
	return profiles[name].LoginSource
}
