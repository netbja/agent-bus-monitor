package rolecfg

import (
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

func TestToolRulesRejections(t *testing.T) {
	long := "Read(" + strings.Repeat("x", 196) + ")"
	many := make([]string, 21)
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
	if len(r.Allow) != others {
		t.Fatalf("master allow = %v, want one per role but master and deploy (%d)", r.Allow, others)
	}
	for _, rule := range r.Allow {
		if strings.Contains(rule, "deploy") {
			t.Fatalf("deploy allowed: %v", r.Allow)
		}
	}
}
