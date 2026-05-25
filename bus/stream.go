// Streams transport for the Agent Bus. A Bus value is bound to one project, so
// every key is namespaced {project}:{kind} and projects sharing a broker never
// collide. This file coexists with the legacy pub/sub helpers in bus.go during
// the migration; the binaries are switched over (and the old API removed) in
// later phases.
package bus

import (
	"regexp"
	"strings"
)

const streamMaxLen = 1000

// cmd entry types carried in the {project}:cmd stream "type" field.
const (
	CmdDirective = "directive" // from hermes; gated by the pilot lease
	CmdChallenge = "challenge" // from a peer; opens a 4-eyes gate on the target
	CmdReply     = "reply"     // response to a challenge, correlated by ref
	CmdVerdict   = "verdict"   // closes a challenge, correlated by ref
)

var validCmdTypes = map[string]bool{
	CmdDirective: true, CmdChallenge: true, CmdReply: true, CmdVerdict: true,
}

// nameRE bounds project slugs and agent names: lowercase, starts with a letter,
// 1–32 chars of [a-z0-9_-]. Replaces the old hardcoded ValidAgents allowlist.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// ValidName reports whether s is an acceptable project or agent identifier.
func ValidName(s string) bool { return nameRE.MatchString(s) }

// The entire channel convention: stream key is {project}:{kind}.
func StreamKey(project, kind string) string { return project + ":" + kind }
func PilotKey(project string) string        { return project + ":pilot" }
func GateKey(project, agent string) string  { return project + ":gate:" + agent }

// Event is a parsed stream entry. Which fields are populated depends on Kind.
type Event struct {
	ID      string // redis stream entry id
	Project string
	Kind    string // status | report | notify | cmd
	Agent   string // status/report: the author
	From    string // notify/cmd: the sender
	Target  string // cmd: the addressed agent
	State   string // status: working|idle|blocked|done
	RKind   string // report: note|auto
	Type    string // cmd: directive|challenge|reply|verdict
	Ref     string // cmd: correlation id
	Message string // status/report/notify text, or the cmd command
}

// ParseEntry turns a raw stream entry into an Event. The kind is derived from
// the stream-key suffix ({project}:{kind}); fields are read per kind. This is
// the Streams analog of the legacy Parse in bus.go.
func ParseEntry(streamKey, id string, fields map[string]string) Event {
	project, kind := splitStreamKey(streamKey)
	e := Event{ID: id, Project: project, Kind: kind}
	switch kind {
	case "status":
		e.Agent, e.State, e.Message = fields["agent"], fields["state"], fields["message"]
	case "report":
		e.Agent, e.RKind, e.Message = fields["agent"], fields["kind"], fields["message"]
	case "notify":
		e.From, e.Message = fields["from"], fields["message"]
	case "cmd":
		e.From, e.Target, e.Type, e.Ref, e.Message =
			fields["from"], fields["target"], fields["type"], fields["ref"], fields["command"]
	}
	return e
}

// splitStreamKey splits {project}:{kind} on the last colon. Project slugs never
// contain a colon (see nameRE), so this is unambiguous.
func splitStreamKey(key string) (project, kind string) {
	if i := strings.LastIndex(key, ":"); i >= 0 {
		return key[:i], key[i+1:]
	}
	return "", key
}
