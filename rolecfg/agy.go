package rolecfg

import (
	"encoding/json"
	"strings"
)

// agy (Antigravity CLI, Google) as a builtin client (agy-client spec, checked
// against agy 1.5.3). Its session settings are a file the launcher writes to
// its own cache and passes with --settings: agy uses them for this session
// only and never saves them.

// agyPermissions are the profile's permission values: default asks before
// each tool use; accept-edits, auto and plan are agy's --mode; skip is
// --dangerously-skip-permissions.
var agyPermissions = []string{"default", "accept-edits", "auto", "plan", "skip"}

// agyEfforts are the --effort values agy's --help lists.
var agyEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// agySession is the --settings object. Only keys agy 1.5.3 accepts: an
// unknown one makes agy refuse to start (spec §3.3).
type agySession struct {
	// ToolPermission is always written: an omitted key inherits the person's
	// own settings, which may say always-proceed; the session value wins.
	ToolPermission          string `json:"toolPermission"`
	AllowNonWorkspaceAccess bool   `json:"allowNonWorkspaceAccess"`
	Permissions             struct {
		// Allow is always [] : a session list replaces the person's, so a role
		// inherits none of the commands they allowed for their own use.
		Allow []string `json:"allow"`
		// Deny is omitted when nothing translates: an empty session list would
		// replace, and so drop, the person's own denies.
		Deny []string `json:"deny,omitempty"`
	} `json:"permissions"`
}

// agySettings is the session JSON for the role's tool rules, and the deny
// rules that have no agy form (named at launch, never dropped silently).
// Allow and ask rules are not translated (v1): the session asks before
// every tool use unless the profile's mode says otherwise.
func agySettings(rules ToolRules) (string, []string) {
	var s agySession
	s.ToolPermission = "request-review"
	s.Permissions.Allow = []string{}
	var untranslated []string
	for _, r := range rules.Deny {
		if d, ok := agyDeny(r); ok {
			s.Permissions.Deny = append(s.Permissions.Deny, d)
		} else {
			untranslated = append(untranslated, r)
		}
	}
	b, _ := json.Marshal(s) // strings and a bool: never fails
	return string(b), untranslated
}

// agyDeny is a manifest deny in agy's form: Bash(<prefix>), Bash(<prefix>*),
// Bash(<prefix> *) or Bash(<prefix>:*) is command(<prefix>). Anything else
// (a file rule, a * inside the pattern, a bare tool name) has no agy form.
func agyDeny(rule string) (string, bool) {
	body, ok := strings.CutPrefix(rule, "Bash(")
	if !ok || !strings.HasSuffix(body, ")") {
		return "", false
	}
	body = strings.TrimSuffix(body, ")")
	if strings.HasSuffix(body, ":*") {
		body = strings.TrimSuffix(body, ":*")
	} else {
		body = strings.TrimSuffix(body, "*")
	}
	body = strings.TrimSpace(body)
	if body == "" || strings.ContainsAny(body, "*()") {
		return "", false
	}
	return "command(" + body + ")", true
}

// agyArgs are the flags a profile's options give agy, in the spec's order
// (§4.3): the mode or the skip, the effort, the sandbox.
func agyArgs(p Profile) []string {
	var out []string
	switch perm := p.Options["permission"]; perm {
	case "", "default":
	case "skip":
		out = append(out, "--dangerously-skip-permissions")
	default:
		out = append(out, "--mode", perm)
	}
	if p.Reasoning != "" {
		out = append(out, "--effort", p.Reasoning)
	}
	if p.Options["sandbox"] == "true" {
		out = append(out, "--sandbox")
	}
	return out
}

// agyNeeded are the --help flags an agy launch passes.
func agyNeeded(p Profile) []string {
	needed := []string{"--model", "--settings", "--prompt-interactive"}
	switch perm := p.Options["permission"]; perm {
	case "", "default":
	case "skip":
		needed = append(needed, "--dangerously-skip-permissions")
	default:
		needed = append(needed, "--mode")
	}
	if p.Reasoning != "" {
		needed = append(needed, "--effort")
	}
	if p.Options["sandbox"] == "true" {
		needed = append(needed, "--sandbox")
	}
	return needed
}

// agyHelpHas reports whether agy's --help lists flag as a word of its own:
// "--mode" is inside "--model", so a substring would always find it.
func agyHelpHas(help, flag string) bool {
	for _, w := range strings.Fields(help) {
		if w == flag {
			return true
		}
	}
	return false
}
