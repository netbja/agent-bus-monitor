package rolecfg

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ToolRules are Claude Code permission rules a role's session gets at launch
// (v0.17, spec specs/2026-10-17-role-tool-rules-design.md):
// passed through unchanged in one --settings JSON.
type ToolRules struct {
	Allow []string `json:"allow,omitempty"`
	Ask   []string `json:"ask,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// Empty reports whether no rule is set.
func (r ToolRules) Empty() bool { return len(r.Allow)+len(r.Ask)+len(r.Deny) == 0 }

// DeliverDeny is the deny every role but master and docs carries in its own
// rules (forge-delivery answer 3: only they push and open requests), never in
// the shared ones, which would deny them too. The spellings after the
// project's global flag are rendered at launch with its name
// (ProjectLaunchRules): a wildcard there would swallow "report coder ..." and
// deny the word deliver in any message (foureyes). Defence in depth, not a
// boundary: the client matches command text.
var DeliverDeny = []string{"Bash(agentbus deliver *)"}

// DeliverProjectDenies are agentbus deliver after project's own global flag,
// each spelling expandArgs reads before a command, alone or followed by the
// host's (a message after --host is caught too: rare, and a residual). Another
// project's name is not covered: agentbus deliver refuses it itself
// (MayDeliver), the rule these patterns only echo.
func DeliverProjectDenies(project string) []string {
	var out []string
	for _, p := range []string{"--project " + project, "--project=" + project, "-p " + project} {
		out = append(out, "Bash(agentbus "+p+" deliver *)")
		for _, h := range []string{"--host *", "--host=*", "-H *"} {
			out = append(out, "Bash(agentbus "+p+" "+h+" deliver *)")
		}
	}
	return out
}

// checkDelivers: delivers is a boolean, and a role that delivers does not
// deny agentbus deliver in its own rules.
func checkDelivers(role string, info map[string]interface{}, own ToolRules) error {
	v, exists := info["delivers"]
	if !exists {
		return nil
	}
	d, ok := v.(bool)
	if !ok {
		return fmt.Errorf("role %s: delivers must be a boolean", role)
	}
	if d && HoldsDeliverDeny(own) {
		return fmt.Errorf("role %s: delivers = true but its tool_rules deny agentbus deliver", role)
	}
	return nil
}

// NotifyWriteDenies are the shared denies of the notifications subcommands
// that write notify.toml or show a topic (notify-guided-setup §2): every
// role's, in the shared [tool_rules], and a drafted role's own. Plain forms
// only: "agentbus * notifications" would deny a message that mentions them;
// the commands refuse an agent caller themselves. status and test are not
// here (test has its own deny, status reveals no secret).
var NotifyWriteDenies = []string{"Bash(agentbus notifications add*)", "Bash(agentbus notifications remove*)",
	"Bash(agentbus notifications rename*)", "Bash(agentbus notifications setup*)", "Bash(agentbus notifications show-topic*)"}

// NotifyProjectDenies are those subcommands after project's own global flag,
// added at launch to a role that holds NotifyWriteDenies (as deliver's). The
// --host spellings are not added: notifications takes no broker, and 45 more
// rules per launch would buy only that residual.
func NotifyProjectDenies(project string) []string {
	var out []string
	for _, d := range NotifyWriteDenies {
		rest := strings.TrimPrefix(d, "Bash(agentbus ")
		for _, p := range []string{"--project " + project, "--project=" + project, "-p " + project} {
			out = append(out, "Bash(agentbus "+p+" "+rest)
		}
	}
	return out
}

func holdsAll(r ToolRules, rules []string) bool {
	for _, d := range rules {
		if !slices.Contains(r.Deny, d) {
			return false
		}
	}
	return true
}

// HoldsDeliverDeny: r denies agentbus deliver.
func HoldsDeliverDeny(r ToolRules) bool {
	for _, d := range DeliverDeny {
		if !slices.Contains(r.Deny, d) {
			return false
		}
	}
	return true
}

const (
	maxRuleBytes = 200
	maxRules     = 64 // v0.17.3 shared rules need Read and Edit forms of each deny; forge-delivery adds two to infra's 40.
)

var (
	rulePattern = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_]*|mcp__[A-Za-z0-9_-]+)(\(.*\))?$`)
	starsOnly   = regexp.MustCompile(`^[*\s]*$`)
)

// roleRules reads and validates a role's pops, approval and tool_rules keys.
func roleRules(role string, info map[string]interface{}) (pops bool, approval string, rules ToolRules, err error) {
	if v, exists := info["pops"]; exists {
		b, ok := v.(bool)
		if !ok {
			return false, "", rules, fmt.Errorf("role %s: pops must be a boolean", role)
		}
		pops = b
	}
	if v, exists := info["approval"]; exists {
		if s, ok := v.(string); !ok || s != "person" {
			return false, "", rules, fmt.Errorf(`role %s: approval must be "person"`, role)
		}
		approval = "person"
	}
	raw, exists := info["tool_rules"]
	if !exists {
		return pops, approval, rules, nil
	}
	rules, err = parseToolRules("role "+role+" tool_rules", raw)
	return pops, approval, rules, err
}

func parseToolRules(label string, raw interface{}) (ToolRules, error) {
	var rules ToolRules
	table, ok := asTable(raw)
	if !ok {
		return rules, fmt.Errorf("%s must be a table", label)
	}
	if err := checkKeys(table, set("allow", "ask", "deny"), label); err != nil {
		return rules, err
	}
	seen := map[string]string{}
	for _, list := range []struct {
		name string
		dst  *[]string
	}{{"allow", &rules.Allow}, {"ask", &rules.Ask}, {"deny", &rules.Deny}} {
		v, exists := table[list.name]
		if !exists {
			continue
		}
		items, ok := v.([]interface{})
		if !ok || len(items) > maxRules {
			return rules, fmt.Errorf("%s.%s: invalid rule list", label, list.name)
		}
		for _, item := range items {
			rule, ok := item.(string)
			if !ok || !validRule(rule) {
				return rules, fmt.Errorf("%s.%s: invalid rule %q", label, list.name, fmt.Sprint(item))
			}
			if list.name == "allow" {
				if rule == "Bash" || (strings.HasPrefix(rule, "Bash(") && starsOnly.MatchString(rule[5:len(rule)-1])) {
					return rules, fmt.Errorf("%s.allow: %s would allow every shell command", label, rule)
				}
			}
			if strings.Contains(rule, "agent-spawn") {
				return rules, fmt.Errorf("%s.%s: agent-spawn rules are derived from the manifest", label, list.name)
			}
			if other, dup := seen[rule]; dup {
				return rules, fmt.Errorf("%s: %s in two lists (%s, %s)", label, rule, other, list.name)
			}
			seen[rule] = list.name
			*list.dst = append(*list.dst, rule)
		}
	}
	return rules, nil
}

// mergeToolRules keeps shared rules first, then role rules. Same-list repeats
// across the two tables are harmless and removed; cross-list conflicts remain
// invalid in the effective result.
func mergeToolRules(role string, shared, local ToolRules) (ToolRules, error) {
	out := ToolRules{}
	seen := map[string]string{}
	for _, group := range []ToolRules{shared, local} {
		for _, list := range []struct {
			name string
			src  []string
			dst  *[]string
		}{{"allow", group.Allow, &out.Allow}, {"ask", group.Ask, &out.Ask}, {"deny", group.Deny, &out.Deny}} {
			for _, rule := range list.src {
				if other, exists := seen[rule]; exists {
					if other != list.name {
						return ToolRules{}, fmt.Errorf("role %s: tool_rules: %s in two lists (%s, %s)", role, rule, other, list.name)
					}
					continue
				}
				seen[rule] = list.name
				*list.dst = append(*list.dst, rule)
			}
		}
	}
	return out, nil
}

func validRule(rule string) bool {
	if rule == "" || len(rule) > maxRuleBytes {
		return false
	}
	for _, r := range rule {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return rulePattern.MatchString(rule)
}

// checkAskPermission rejects an ask rule where nothing prompts.
func checkAskPermission(role string, rules ToolRules, client, permission string) error {
	if client == "claude-code" && permission == "bypassPermissions" && len(rules.Ask) > 0 {
		return fmt.Errorf("role %s: tool_rules.ask has no effect with bypassPermissions", role)
	}
	return nil
}

// Approval is "person" when a popping role's session must ask the person
// before it pops role (an ask rule), "" otherwise (and for an unknown role).
func (m *Manifest) Approval(role string) string {
	info, ok := asTable(m.Roles[role])
	if !ok {
		return ""
	}
	_, approval, _, err := roleRules(role, info)
	if err != nil {
		return ""
	}
	return approval
}

// RetireRule is the one form of a pop's end a popping role may run without
// a prompt: the dispatcher's directive, word for word but its values.
func RetireRule(role string) string {
	return "Bash(agentbus retire " + role + " --forget --episode * --reason *)"
}

// LaunchRules are the rules role's session starts with: for a role that pops
// others, one spawn rule per other role in manifest order (allow, or ask when
// that role needs approval), then one retire rule per pop role (allow,
// whatever its approval: pop-auto-retire §6, the person's yes to pop covers
// the end; the exact --forget form only, never --now), then the role's
// declared rules.
func (m *Manifest) LaunchRules(role string) (ToolRules, error) {
	p, err := m.Resolve(role)
	if err != nil {
		return ToolRules{}, err
	}
	var derived ToolRules
	if p.Pops {
		for _, other := range m.roleOrder {
			if other == role {
				continue
			}
			rule := "Bash(agent-spawn " + other + " *)"
			if m.Approval(other) == "person" {
				derived.Ask = append(derived.Ask, rule)
			} else {
				derived.Allow = append(derived.Allow, rule)
			}
		}
		for _, other := range m.roleOrder {
			if other != role && m.RoleTier(other) == "pop" {
				derived.Allow = append(derived.Allow, RetireRule(other))
			}
		}
	}
	out, err := mergeToolRules(role, m.SharedToolRules, derived)
	if err != nil {
		return ToolRules{}, err
	}
	info, _ := asTable(m.Roles[role])
	_, _, local, err := roleRules(role, info)
	if err != nil {
		return ToolRules{}, err
	}
	merged, err := mergeToolRules(role, out, local)
	if err != nil {
		return ToolRules{}, err
	}
	out = merged
	return out, nil
}

// SpawnRules are role's agent-spawn rules (LaunchRules') as the roles they
// name: allow, and ask (the person approves each pop). Both nil for a role
// that does not pop; a role missing from the manifest has no rule at all, so
// its spawn prompts (roles doctor shows it, roles-doctor-pop-rules).
func (m *Manifest) SpawnRules(role string) (allow, ask []string, err error) {
	r, err := m.LaunchRules(role)
	if err != nil {
		return nil, nil, err
	}
	names := func(rules []string) (out []string) {
		for _, rule := range rules {
			if s, ok := strings.CutPrefix(rule, "Bash(agent-spawn "); ok {
				if name, ok := strings.CutSuffix(s, " *)"); ok {
					out = append(out, name)
				}
			}
		}
		return out
	}
	return names(r.Allow), names(r.Ask), nil
}

// MayDeliver is the rule agentbus deliver reads itself, whatever the
// command's spelling (reviewer-codex, master: no pattern covers every project
// name or flag order): a role delivers only with delivers = true (master and
// docs, forge-delivery answer 3), so a role added without it never does; a
// name that is no role (a person) may.
func (m *Manifest) MayDeliver(role string) bool {
	info, ok := asTable(m.Roles[role])
	if !ok {
		return true
	}
	d, _ := info["delivers"].(bool)
	return d
}

// ProjectLaunchRules are LaunchRules for a launch in project: a role that
// denies agentbus deliver also denies it after the project's global flag,
// with the project's name (DeliverProjectDenies).
func (m *Manifest) ProjectLaunchRules(role, project string) (ToolRules, error) {
	r, err := m.LaunchRules(role)
	if err != nil {
		return r, err
	}
	var add []string
	if HoldsDeliverDeny(r) {
		add = append(add, DeliverProjectDenies(project)...)
	}
	if holdsAll(r, NotifyWriteDenies) {
		add = append(add, NotifyProjectDenies(project)...)
	}
	for _, d := range add {
		if !slices.Contains(r.Deny, d) {
			r.Deny = append(r.Deny, d)
		}
	}
	return r, nil
}
