package rolecfg

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const legacyRules = `[roles.master]
model = "sonnet"
permission = "acceptEdits"
tier = "boot"
pops = true
[roles.master.tool_rules]
allow = ["Read"]
[roles.coder]
model = "opus"
permission = "bypassPermissions"
tier = "boot"
[roles.deploy]
model = "sonnet"
permission = "bypassPermissions"
tier = "pop"
approval = "person"
[roles.architect]
model = "opus"
permission = "bypassPermissions"
tier = "pop"
`

const schema2Rules = `schema_version = 2
[profiles.claude]
client = "claude-code"
provider = "anthropic"
model = "sonnet"
options = { permission = "acceptEdits" }
[roles.master]
profile = "claude"
tier = "boot"
pops = true
approval = "person"
[roles.master.tool_rules]
deny = ["WebFetch"]
`

func TestToolRulesAcceptedInBothSchemas(t *testing.T) {
	m, err := Load(writeConfig(t, legacyRules))
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Resolve("master")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Pops || p.Approval != "" || !reflect.DeepEqual(p.ToolRules.Allow, []string{"Read"}) {
		t.Fatalf("legacy master = pops %v approval %q rules %+v", p.Pops, p.Approval, p.ToolRules)
	}
	if got := m.Approval("deploy"); got != "person" {
		t.Fatalf("Approval(deploy) = %q", got)
	}
	m, err = Load(writeConfig(t, schema2Rules))
	if err != nil {
		t.Fatal(err)
	}
	p, err = m.Resolve("master")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Pops || p.Approval != "person" || !reflect.DeepEqual(p.ToolRules.Deny, []string{"WebFetch"}) {
		t.Fatalf("schema 2 master = pops %v approval %q rules %+v", p.Pops, p.Approval, p.ToolRules)
	}
}

func TestSharedToolRulesMergeBeforeRoleRulesAndDeduplicate(t *testing.T) {
	path := writeConfig(t, `schema_version = 2
[tool_rules]
allow = ["Read", "Glob"]
deny = ["WebFetch"]
[profiles.claude]
client = "claude-code"
provider = "anthropic"
model = "sonnet"
options = { permission = "acceptEdits" }
[roles.master]
profile = "claude"
tier = "boot"
pops = true
[roles.master.tool_rules]
allow = ["Glob", "Write"]
deny = ["WebFetch", "Bash(rm *)"]
`)
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := m.LaunchRules("master")
	if err != nil {
		t.Fatal(err)
	}
	wantAllow := append([]string{"Read", "Glob"}, "Write")
	if !reflect.DeepEqual(rules.Allow, wantAllow) {
		t.Fatalf("allow=%v want %v", rules.Allow, wantAllow)
	}
	if !reflect.DeepEqual(rules.Deny, []string{"WebFetch", "Bash(rm *)"}) {
		t.Fatalf("deny=%v", rules.Deny)
	}
	resolved, err := m.Resolve("master")
	if err != nil || !reflect.DeepEqual(resolved.ToolRules.Allow, []string{"Read", "Glob", "Write"}) {
		t.Fatalf("resolved rules=%+v err=%v", resolved.ToolRules, err)
	}
}

func TestSharedToolRulesValidationAppliesToMergedResult(t *testing.T) {
	base := `schema_version = 2
[tool_rules]
%s
[profiles.claude]
client = "claude-code"
provider = "anthropic"
model = "sonnet"
options = { permission = "%s" }
[roles.coder]
profile = "claude"
tier = "boot"
%s
`
	for _, tc := range []struct{ name, shared, permission, local, want string }{
		{"unknown shared key", "extra = [\"Read\"]", "acceptEdits", "", "unknown"},
		{"cross-list conflict", "allow = [\"Read\"]", "acceptEdits", "[roles.coder.tool_rules]\ndeny = [\"Read\"]", "in two lists"},
		{"ask forbidden with bypass", "ask = [\"Read\"]", "bypassPermissions", "", "has no effect with bypassPermissions"},
		{"same-list duplicate is allowed across tables", "deny = [\"Read\"]", "bypassPermissions", "[roles.coder.tool_rules]\ndeny = [\"Read\"]", ""},
		{"invalid shared rule", "deny = [\"9bad\"]", "acceptEdits", "", "invalid rule"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Load(writeConfig(t, fmt.Sprintf(base, tc.shared, tc.permission, tc.local)))
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				got, err := m.LaunchRules("coder")
				if err != nil || !reflect.DeepEqual(got.Deny, []string{"Read"}) {
					t.Fatalf("rules=%+v err=%v", got, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want substring %q", err, tc.want)
			}
		})
	}
}

func TestToolRulesRejections(t *testing.T) {
	long := "Read(" + strings.Repeat("x", 196) + ")"
	many := make([]string, 41)
	for i := range many {
		many[i] = `"Read"`
	}
	for _, c := range []struct{ name, master, want string }{
		{"pops type", `pops = "yes"`, "pops must be a boolean"},
		{"approval value", `approval = "agent"`, `approval must be "person"`},
		{"unknown key", "[roles.master.tool_rules]\nextra = [\"Read\"]", "unknown"},
		{"bare Bash", "[roles.master.tool_rules]\nallow = [\"Bash\"]", "tool_rules.allow: Bash would allow every shell command"},
		{"star Bash", "[roles.master.tool_rules]\nallow = [\"Bash( * )\"]", "tool_rules.allow: Bash( * ) would allow every shell command"},
		{"agent-spawn", "[roles.master.tool_rules]\nallow = [\"Bash(agent-spawn coder *)\"]", "tool_rules.allow: agent-spawn rules are derived from the manifest"},
		{"too long", "[roles.master.tool_rules]\nallow = [\"" + long + "\"]", "invalid rule"},
		{"control char", "[roles.master.tool_rules]\nallow = [\"Read\\n\"]", "invalid rule"},
		{"bad name", "[roles.master.tool_rules]\nallow = [\"9bad\"]", "invalid rule"},
		{"too many", "[roles.master.tool_rules]\nallow = [" + strings.Join(many, ",") + "]", "invalid rule"},
		{"two lists", "[roles.master.tool_rules]\nallow = [\"Read\"]\nask = [\"Read\"]", "in two lists"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, "[roles.master]\nmodel = \"sonnet\"\npermission = \"acceptEdits\"\ntier = \"boot\"\n"+c.master+"\n"))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
	_, err := Load(writeConfig(t, "[roles.coder]\nmodel = \"opus\"\npermission = \"bypassPermissions\"\ntier = \"boot\"\n[roles.coder.tool_rules]\nask = [\"Read\"]\n"))
	if err == nil || !strings.Contains(err.Error(), "tool_rules.ask has no effect with bypassPermissions") {
		t.Fatalf("ask with bypass: %v", err)
	}
}

func TestLaunchRulesDerivesSpawnRules(t *testing.T) {
	m, err := Load(writeConfig(t, legacyRules))
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.LaunchRules("master")
	if err != nil {
		t.Fatal(err)
	}
	want := ToolRules{
		Allow: []string{"Bash(agent-spawn coder *)", "Bash(agent-spawn architect *)", "Read"},
		Ask:   []string{"Bash(agent-spawn deploy *)"},
	}
	if !reflect.DeepEqual(r, want) {
		t.Fatalf("LaunchRules(master) = %+v, want %+v", r, want)
	}
	if r, _ := m.LaunchRules("coder"); !r.Empty() {
		t.Fatalf("a role without pops got %+v", r)
	}
}

// masterUpRules are master's own rules in the shipped manifest (Bernard,
// 2026-10-27): the exact relaunch of this project, nothing broader.
var masterUpRules = []string{"Bash(agentbus up agent-bus-monitor --resume)", "Bash(agentbus up agent-bus-monitor --resume --dry-run)"}

func TestShippedManifestRules(t *testing.T) {
	m, err := Load(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.LaunchRules("master")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Ask, []string{"Bash(agent-spawn deploy *)"}) {
		t.Fatalf("master ask = %v", r.Ask)
	}
	others := 0
	for _, role := range m.roleOrder {
		if role != "master" && role != "deploy" {
			others++
		}
	}
	if len(r.Allow) != others+len(masterUpRules) || !reflect.DeepEqual(r.Allow[others:], masterUpRules) {
		t.Fatalf("master allow = %v, want one per role but master and deploy (%d), then %v", r.Allow, others, masterUpRules)
	}
	for _, role := range m.roleOrder {
		resolved, err := m.Resolve(role)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(resolved.ToolRules.Deny, m.SharedToolRules.Deny) {
			t.Fatalf("%s shared deny rules = %v", role, resolved.ToolRules.Deny)
		}
	}
	for _, rule := range m.SharedToolRules.Deny {
		if strings.Contains(rule, ".env.example") {
			t.Fatalf("example env file is denied: %s", rule)
		}
	}
	for _, rule := range r.Allow {
		if strings.Contains(rule, "deploy") {
			t.Fatalf("deploy allowed: %v", r.Allow)
		}
	}
}
