// Package health defines the deterministic health check catalogue and its
// serializable results. It has no Redis or process dependencies.
package health

import (
	"regexp"
	"sort"
	"time"
	"unicode"
)

type Status string

const (
	OK      Status = "ok"
	Warn    Status = "warn"
	Fail    Status = "fail"
	Unknown Status = "unknown"
)

// Definition is static metadata shared by dispatch, the CLI and busmon.
type Definition struct {
	ID          string
	Title       string
	Interval    time.Duration
	Deadline    time.Duration
	Remediation string
	Order       int
}

// Catalogue contains only checks in the v0.19.0 release split. Later checks
// such as the canary, scanner and project-defined commands are deliberately
// not registered here.
var Catalogue = []Definition{
	{ID: "services", Title: "Services", Interval: time.Minute, Deadline: 5 * time.Second, Remediation: "Inspect the service log; restart only when its restart budget allows. Missions waiting for Git: run git init in the project directory.", Order: 1},
	{ID: "agent-identity", Title: "Agent identity", Interval: time.Minute, Deadline: 5 * time.Second, Remediation: "Re-check agentbus agents and herdr; ask the person before acting on an identity conflict.", Order: 2},
	{ID: "agent-launch", Title: "Agent launch and identity", Interval: 5 * time.Minute, Deadline: 5 * time.Second, Remediation: "Relaunch through agentbus with the configured role; do not edit a live pane.", Order: 3},
	{ID: "model-drift", Title: "Model drift", Interval: 24 * time.Hour, Deadline: 30 * time.Second, Remediation: "Review the result with agentbus roles doctor before changing roles.toml.", Order: 4},
	{ID: "runaway", Title: "Runaway work", Interval: time.Minute, Deadline: 5 * time.Second, Remediation: "Inspect the pane read-only and ask master or the person; never interrupt for this warning.", Order: 5},
	{ID: "cmd-delivery", Title: "Cmd delivery", Interval: time.Minute, Deadline: 5 * time.Second, Remediation: "Inspect the named consumer group; recover delivery only through its normal consumer.", Order: 6},
	{ID: "cmd-targets", Title: "Cmd targets", Interval: 5 * time.Minute, Deadline: 5 * time.Second, Remediation: "Correct the target through the normal sender; do not replay or delete entries automatically.", Order: 7},
	{ID: "quota-freshness", Title: "Quota freshness", Interval: time.Minute, Deadline: 5 * time.Second, Remediation: "Check provider connectivity; leave hold and release decisions to dispatch.", Order: 8},
	{ID: "tracked-secrets", Title: "Tracked secret-like files", Interval: time.Hour, Deadline: 30 * time.Second, Remediation: "Remove the path from version control and rotate any credential that may have been committed.", Order: 9},
	{ID: "redis-password", Title: "Redis exposure and password", Interval: 5 * time.Minute, Deadline: 5 * time.Second, Remediation: "Restrict Redis to loopback; the default password is replaced by ./setup.sh rotate-redis-password (v0.19.1).", Order: 10},
	{ID: "service-env", Title: "Service environment permissions", Interval: 5 * time.Minute, Deadline: 5 * time.Second, Remediation: "Recreate the environment file with agentbus up and verify its permissions without printing it.", Order: 11},
	{ID: "installed-versions", Title: "Installed versions", Interval: 24 * time.Hour, Deadline: 30 * time.Second, Remediation: "Review the documented update path and upgrade only to a known-good version.", Order: 12},
	{ID: "client-updates", Title: "Client updates", Interval: 5 * time.Minute, Deadline: 5 * time.Second, Remediation: "Propose a reload to the person through master (agent-bus-sentinel skill, Client update); never restart a client or type into a pane.", Order: 14},
	{ID: "herdr-probe", Title: "herdr command probe", Interval: 5 * time.Minute, Deadline: 5 * time.Second, Remediation: "Check herdr status; restart it only through its documented service command.", Order: 13},
}

func Definitions() []Definition {
	out := append([]Definition(nil), Catalogue...)
	sort.Slice(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

func ByID(id string) (Definition, bool) {
	for _, d := range Catalogue {
		if d.ID == id {
			return d, true
		}
	}
	return Definition{}, false
}

// Record is one measured check result. Timestamps are Unix milliseconds from
// Redis TIME; Detail must contain only bounded, non-secret facts.
type Record struct {
	ID          string `json:"id"`
	Status      Status `json:"status"`
	Detail      string `json:"detail,omitempty"`
	ObservedAt  int64  `json:"observed_at"`
	Since       int64  `json:"since"`
	Fingerprint string `json:"fingerprint"`
}

type Transition struct {
	ID          string `json:"id"`
	Previous    Status `json:"previous,omitempty"`
	Status      Status `json:"status"`
	Target      string `json:"target,omitempty"`
	At          int64  `json:"at"`
	Fingerprint string `json:"fingerprint"`
	StreamID    string `json:"stream_id,omitempty"`
}

type Note struct {
	ID     string `json:"id"`
	Author string `json:"author"`
	At     int64  `json:"at"`
	Text   string `json:"text"`
}

var sensitiveText = regexp.MustCompile(`(?i)(\b(?:password|token|secret|api[_-]?key|authorization)\b\s*[:=]\s*\S+|-----BEGIN [A-Z ]*PRIVATE KEY-----|https?://[^/\s:@]+:[^/\s@]+@|\b(?:sk-ant-|gh[pousr]_|xox[baprs]-)[A-Za-z0-9_-]{12,})`)

// SafeText rejects control characters, oversized text and common credential
// shapes before details or notes are persisted or displayed. Callers should
// prefer fixed diagnostic phrases and never pass command output or file data.
func SafeText(s string) bool {
	runes := []rune(s)
	if len(runes) > 600 || sensitiveText.MatchString(s) {
		return false
	}
	for _, r := range runes {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}

// EffectiveStatus maps absent or stale measurements to unknown. A measurement
// is stale after two check intervals.
func EffectiveStatus(r Record, nowMS int64, interval time.Duration) Status {
	if r.ID == "" || r.ObservedAt <= 0 || interval <= 0 || nowMS-r.ObservedAt >= interval.Milliseconds()*2 {
		return Unknown
	}
	switch r.Status {
	case OK, Warn, Fail, Unknown:
		return r.Status
	default:
		return Unknown
	}
}
