package rolecfg

import (
	"fmt"
	"regexp"
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

const (
	maxRuleBytes = 200
	maxRules     = 20
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
	table, ok := asTable(raw)
	if !ok {
		return false, "", rules, fmt.Errorf("role %s: tool_rules must be a table", role)
	}
	if err := checkKeys(table, set("allow", "ask", "deny"), "role "+role+" tool_rules"); err != nil {
		return false, "", rules, err
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
			return false, "", rules, fmt.Errorf("role %s: tool_rules.%s: invalid rule list", role, list.name)
		}
		for _, item := range items {
			rule, ok := item.(string)
			if !ok || !validRule(rule) {
				return false, "", rules, fmt.Errorf("role %s: tool_rules.%s: invalid rule %q", role, list.name, fmt.Sprint(item))
			}
			if list.name == "allow" {
				if rule == "Bash" || (strings.HasPrefix(rule, "Bash(") && starsOnly.MatchString(rule[5:len(rule)-1])) {
					return false, "", rules, fmt.Errorf("role %s: tool_rules.allow: %s would allow every shell command", role, rule)
				}
			}
			if strings.Contains(rule, "agent-spawn") {
				return false, "", rules, fmt.Errorf("role %s: tool_rules.%s: agent-spawn rules are derived from the manifest", role, list.name)
			}
			if other, dup := seen[rule]; dup {
				return false, "", rules, fmt.Errorf("role %s: tool_rules: %s in two lists (%s, %s)", role, rule, other, list.name)
			}
			seen[rule] = list.name
			*list.dst = append(*list.dst, rule)
		}
	}
	return pops, approval, rules, nil
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

// LaunchRules are the rules role's session starts with: for a role that pops
// others, one spawn rule per other role in manifest order (allow, or ask when
// that role needs approval), then the role's declared rules.
func (m *Manifest) LaunchRules(role string) (ToolRules, error) {
	p, err := m.Resolve(role)
	if err != nil {
		return ToolRules{}, err
	}
	var out ToolRules
	if p.Pops {
		for _, other := range m.roleOrder {
			if other == role {
				continue
			}
			rule := "Bash(agent-spawn " + other + " *)"
			if m.Approval(other) == "person" {
				out.Ask = append(out.Ask, rule)
			} else {
				out.Allow = append(out.Allow, rule)
			}
		}
	}
	out.Allow = append(out.Allow, p.ToolRules.Allow...)
	out.Ask = append(out.Ask, p.ToolRules.Ask...)
	out.Deny = append(out.Deny, p.ToolRules.Deny...)
	return out, nil
}
