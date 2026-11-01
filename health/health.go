// Package health defines the deterministic health check catalogue and its
// serializable results. It has no Redis or process dependencies.
package health

import (
	"regexp"
	"sort"
	"strings"
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
	// Where a measured problem goes on busmon's F5 (busmon pages, Health):
	// Person are the statuses only the person can fix, with Command the one
	// command they run; Reasons the evaluator's codes (Outcome.Reason) that
	// are the person's whatever the status, each with its command; every
	// other warn or fail is in hand, dealt with by Handler. A fixed property
	// of the check, its status and its code, never read from its detail.
	Person  []Status
	Command string
	Reasons map[string]string
	Handler string
}

// Place is where a check stands for the person.
type Place string

const (
	PlaceForYou      Place = "for you"      // only the person can fix it
	PlaceInHand      Place = "in hand"      // something already deals with it
	PlaceFine        Place = "fine"         // measured ok
	PlaceNotMeasured Place = "not measured" // no current measurement: never fine
)

// PlaceOf is the place of rec read as status s (its effective status): a
// warn or fail whose reason is one of the check's Reasons is for you,
// whatever the check's usual place.
func (d Definition) PlaceOf(rec Record, s Status) Place {
	if _, ok := d.Reasons[rec.Reason]; ok && rec.Reason != "" && (s == Warn || s == Fail) {
		return PlaceForYou
	}
	return d.Place(s)
}

// CommandOf is the person's command for rec: its reason's, else the check's.
func (d Definition) CommandOf(rec Record) string {
	if c, ok := d.Reasons[rec.Reason]; ok && rec.Reason != "" {
		return c
	}
	return d.Command
}

// Place is the place of status s for this check.
func (d Definition) Place(s Status) Place {
	switch s {
	case OK:
		return PlaceFine
	case Warn, Fail:
		for _, p := range d.Person {
			if p == s {
				return PlaceForYou
			}
		}
		return PlaceInHand
	}
	return PlaceNotMeasured
}

// Catalogue contains only checks in the v0.19.0 release split. Later checks
// such as the canary, scanner and project-defined commands are deliberately
// not registered here.
var Catalogue = []Definition{
	{ID: "services", Title: "Services", Interval: time.Minute, Deadline: 5 * time.Second, Remediation: "Inspect the service log; restart only when its restart budget allows. Missions waiting for Git: run git init in the project directory.", Order: 1, Handler: "sentinel",
		Reasons: map[string]string{ // a spent kind has given up: restarting it interrupts nothing
			ReasonBudgetSpent:         "agentbus --project <project> service restart",
			ReasonBudgetSpentDispatch: "agentbus --project <project> service restart dispatch",
			ReasonBudgetSpentMissions: "agentbus --project <project> service restart missions",
			ReasonWaitingGit:          "git init"}},
	{ID: "agent-identity", Title: "Agent identity", Interval: time.Minute, Deadline: 5 * time.Second, Remediation: "Re-check agentbus agents and herdr; ask the person before acting on an identity conflict.", Order: 2, Handler: "master"},
	{ID: "agent-launch", Title: "Agent launch and identity", Interval: 5 * time.Minute, Deadline: 5 * time.Second, Remediation: "Relaunch through agentbus with the configured role; do not edit a live pane.", Order: 3, Handler: "master"},
	{ID: "model-drift", Title: "Model drift", Interval: 24 * time.Hour, Deadline: 30 * time.Second, Remediation: "Review the result with agentbus roles doctor before changing roles.toml.", Order: 4, Person: []Status{Warn, Fail}, Command: "agentbus roles doctor"},
	{ID: "runaway", Title: "Runaway work", Interval: time.Minute, Deadline: 5 * time.Second, Remediation: "Inspect the pane read-only and ask master or the person; never interrupt for this warning.", Order: 5, Handler: "master"},
	{ID: "cmd-delivery", Title: "Cmd delivery", Interval: time.Minute, Deadline: 5 * time.Second, Remediation: "Inspect the named consumer group; recover delivery only through its normal consumer.", Order: 6, Handler: "master"},
	{ID: "cmd-targets", Title: "Cmd targets", Interval: 5 * time.Minute, Deadline: 5 * time.Second, Remediation: "Correct the target through the normal sender; do not replay or delete entries automatically.", Order: 7, Handler: "master"},
	{ID: "quota-freshness", Title: "Quota freshness", Interval: time.Minute, Deadline: 5 * time.Second, Remediation: "Check provider connectivity; leave hold and release decisions to dispatch.", Order: 8, Handler: "dispatch"},
	{ID: "tracked-secrets", Title: "Tracked secret-like files", Interval: time.Hour, Deadline: 30 * time.Second, Remediation: "Remove the path from version control and rotate any credential that may have been committed.", Order: 9, Person: []Status{Warn, Fail}, Command: "git rm --cached <path>"},
	{ID: "redis-password", Title: "Redis exposure and password", Interval: 5 * time.Minute, Deadline: 5 * time.Second, Remediation: "Restrict Redis to loopback. The historical default password is replaced by the person, when it suits them, with ./setup.sh rotate-redis-password (an install on a fresh broker already gets a random one); an exported REDIS_PASSWORD or REDIS_URL overrides the password file. Sentinel never rotates it.", Order: 10, Person: []Status{Warn, Fail}, Command: "./setup.sh rotate-redis-password"},
	{ID: "service-env", Title: "Service environment permissions", Interval: 5 * time.Minute, Deadline: 5 * time.Second, Remediation: "Recreate the environment file with agentbus up and verify its permissions without printing it.", Order: 11, Person: []Status{Warn, Fail}, Command: "agentbus up <project>"},
	{ID: "installed-versions", Title: "Installed versions", Interval: 24 * time.Hour, Deadline: 30 * time.Second, Remediation: "Review the documented update path and upgrade only to a known-good version.", Order: 12, Person: []Status{Warn, Fail}, Command: "./setup.sh update"},
	{ID: "client-updates", Title: "Client updates", Interval: 5 * time.Minute, Deadline: 5 * time.Second, Remediation: "Propose a reload to the person through master (agent-bus-sentinel skill, Client update); never restart a client or type into a pane.", Order: 14, Handler: "sentinel"},
	{ID: "context", Title: "Context fill", Interval: time.Minute, Deadline: 5 * time.Second, Remediation: "The dispatcher told the agent to commit WIP and send master a hand-off; let it reach a stopping point, then the person reloads it (/clear or a new session). Never type into its pane.", Order: 15, Handler: "dispatch"},
	{ID: "redis-version", Title: "Redis version", Interval: time.Hour, Deadline: 5 * time.Second, Remediation: "Behind in its major: the person updates the broker when it suits them (docker compose pull redis, then docker compose up -d redis), to the latest release of the same major. A newer major is never a warning: changing major is the person's decision after reading its release notes. Sentinel never restarts or upgrades the broker, it only reports.", Order: 16, Person: []Status{Warn, Fail}, Command: "docker compose pull redis"},
	{ID: "github", Title: "GitHub reachable", Interval: 15 * time.Minute, Deadline: 10 * time.Second, Remediation: "Check your git remote and credentials; try: git ls-remote <your remote URL>. The check measures reachability by SSH and HTTPS from the project's services; it makes no claim about pushing, and never fails.", Order: 17, Person: []Status{Warn, Fail}, Command: "git ls-remote <your remote URL>"},
	// The stall guard (spec 2026-11-01-stall-guard-design.md; judgement in stall.go).
	{ID: "team-stall", Title: "Team stall", Interval: time.Minute, Deadline: 5 * time.Second, Remediation: "Every agent is idle with nothing open and work ready in the plan. Sentinel tells master the next ready task; if master cannot act, the person is told. When only your own tasks are ready, read the plan.", Order: 18, Handler: "sentinel",
		Reasons: map[string]string{"waiting-person": "agentbus plan"}},
	{ID: "clear-stall", Title: "Clear after hand-off", Interval: time.Minute, Deadline: 5 * time.Second, Remediation: "An agent handed off and was not cleared; the detail gives the dispatcher's reason. When its pane is the one you have focused, switch to another pane and the dispatcher clears it.", Order: 19, Handler: "dispatch",
		Reasons: map[string]string{"pane-focused": "switch focus to another pane"}},
	{ID: "quiet-agent", Title: "Quiet agent", Interval: time.Minute, Deadline: 5 * time.Second, Remediation: "A directive reached an agent that has written nothing since; master resends it or asks the agent.", Order: 20, Handler: "master"},
	{ID: "disk", Title: "Disk space", Interval: 5 * time.Minute, Deadline: 10 * time.Second, Remediation: "Free space on the filesystems agent-bus depends on (the broker's data, home, the Go cache, the repository and its worktrees, the temp dir). Look first: du -sh ~/.cache/go-build ~/.npm ~/.cache/pip shows the biggest known caches. Then, your choice: go clean -cache empties the Go build cache, which rebuilds itself. Sentinel never deletes anything: freeing space is the person's act.", Order: 21, Person: []Status{Warn, Fail}, Command: "du -sh ~/.cache/go-build ~/.npm ~/.cache/pip"},
	{ID: "herdr-probe", Title: "herdr command probe", Interval: 5 * time.Minute, Deadline: 5 * time.Second, Remediation: "Check herdr status; restart it only through its documented service command.", Order: 13, Person: []Status{Fail}, Command: "herdr status", Handler: "dispatch"},
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
	Reason      string `json:"reason,omitempty"` // the evaluator's Outcome.Reason, one of the check's Reasons
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

// sensitiveText: a credential-looking name followed by a value (as a word, or
// glued to a prefix as in REDIS_PASSWORD or PGPASSWORD, or an env-style name
// ending in _KEY, _PASS or _PWD),
// any URL scheme with userinfo, a private key block, the known token prefixes.
var sensitiveText = regexp.MustCompile(`(?i)(\b[a-z0-9_]*(?:password|passwd|token|secret|api[_-]?key|authorization)\b["']?\s*[:=]\s*\S+|\b[a-z0-9]+(?:_[a-z0-9]+)*_(?:key|pass|pwd)\b["']?\s*[:=]\s*\S+|-----BEGIN [A-Z ]*PRIVATE KEY-----|\b[a-z][a-z0-9+.-]*://[^/\s:@]*:[^/\s@]+@|\b(?:sk-ant-|gh[pousr]_|github_pat_|xox[baprs]-)[A-Za-z0-9_-]{12,})`)

// authScheme finds Bearer or Basic with a candidate token after it; the token
// counts only if it reads as one (bearerToken), so prose passes.
var authScheme = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+([A-Za-z0-9._~+/=-]{8,})`)

// bearerToken: 16 characters or more, or a digit or one of ._~+= in it.
// "Basic authentication" and "basic end-to-end" (letters joined by - or /)
// are prose; "dXNlcjpwYXNzd29yZA==" is a credential.
func bearerToken(tok string) bool {
	return len(tok) >= 16 || strings.ContainsAny(tok, "0123456789._~+=")
}

// Sensitive reports whether s carries a common credential shape. Best effort:
// a secret in a shape not listed passes.
func Sensitive(s string) bool {
	if sensitiveText.MatchString(s) {
		return true
	}
	for _, m := range authScheme.FindAllStringSubmatch(s, -1) {
		if bearerToken(m[1]) {
			return true
		}
	}
	return false
}

// Redacted replaces a whole field that Redact judges sensitive.
const Redacted = "[redacted]"

// Redact returns s unchanged, or Redacted on any credential shape: the whole
// field, never the matched span, which would leave the rest of the secret.
func Redact(s string) string {
	if Sensitive(s) {
		return Redacted
	}
	return s
}

// SafeText rejects control characters, oversized text and common credential
// shapes before details or notes are persisted or displayed. Callers should
// prefer fixed diagnostic phrases and never pass command output or file data.
func SafeText(s string) bool {
	runes := []rune(s)
	if len(runes) > 600 || Sensitive(s) {
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
