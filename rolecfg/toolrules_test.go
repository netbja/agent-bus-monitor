package rolecfg

import (
	"fmt"
	"os"
	"reflect"
	"slices"
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
	many := make([]string, maxRules+1)
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

// reloadAsDeny are the shared denies on an agent passing --as to reload
// (lifecycle part C, busmon-reload-deny): a person's --as skips the caller
// table and AGENT_BUS_RELOAD's opt-outs, so no agent may type one. Defence
// in depth, not a boundary: the client matches command text.
var reloadAsDeny = []string{"Bash(agentbus reload *--as*)", "Bash(agentbus * reload *--as*)"}

// masterUpRules are master's own rules in the shipped manifest (Charlie,
// 2026-10-27): the exact relaunch of this project, and (lifecycle part C) the
// exact reload of sentinel, nothing broader.
var masterUpRules = []string{"Bash(agentbus up agent-bus-monitor --resume)", "Bash(agentbus up agent-bus-monitor --resume --dry-run)",
	"Bash(agentbus reload sentinel)", "Bash(agentbus reload sentinel --dry-run)", "Bash(agentbus deliver *)"}

func TestShippedManifestRules(t *testing.T) {
	m, err := Load(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.LaunchRules("master")
	if err != nil {
		t.Fatal(err)
	}
	// The roles popped only through the person (approval = "person": deploy,
	// infra) are asked, in manifest order; every other role is allowed.
	var asked []string
	others := 0
	for _, role := range m.roleOrder {
		p, err := m.Resolve(role)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case role == "master":
		case p.Approval == "person":
			asked = append(asked, "Bash(agent-spawn "+role+" *)")
		default:
			others++
		}
	}
	if want := []string{"Bash(agent-spawn deploy *)", "Bash(agent-spawn infra *)"}; !reflect.DeepEqual(asked, want) || !reflect.DeepEqual(r.Ask, want) {
		t.Fatalf("master ask = %v, person-approved roles %v, want %v", r.Ask, asked, want)
	}
	if len(r.Allow) != others+len(masterUpRules) || !reflect.DeepEqual(r.Allow[others:], masterUpRules) {
		t.Fatalf("master allow = %v, want one per role but master and the person-approved ones (%d), then %v", r.Allow, others, masterUpRules)
	}
	for _, role := range m.roleOrder {
		resolved, err := m.Resolve(role)
		if err != nil {
			t.Fatal(err)
		}
		// Every role carries the shared denies first; a role may add its own
		// after them (infra's secrets), never drop one.
		d := resolved.ToolRules.Deny
		if len(d) < len(m.SharedToolRules.Deny) || !reflect.DeepEqual(d[:len(m.SharedToolRules.Deny)], m.SharedToolRules.Deny) {
			t.Fatalf("%s shared deny rules = %v", role, d)
		}
	}
	for _, rule := range m.SharedToolRules.Deny {
		if strings.Contains(rule, ".env.example") {
			t.Fatalf("example env file is denied: %s", rule)
		}
	}
	for _, rule := range r.Allow {
		for _, a := range asked {
			if rule == a {
				t.Fatalf("%s allowed: %v", a, r.Allow)
			}
		}
	}
	for _, rule := range reloadAsDeny {
		if !slices.Contains(m.SharedToolRules.Deny, rule) {
			t.Fatalf("shared deny %v lacks %s", m.SharedToolRules.Deny, rule)
		}
	}
	// Only master and docs deliver (forge-delivery answer 3): their own allow,
	// and both denies in every other role's own rules, never in the shared
	// ones, which would deny master and docs too.
	for _, rule := range DeliverDeny {
		if slices.Contains(m.SharedToolRules.Deny, rule) {
			t.Fatalf("shared deny holds %s", rule)
		}
	}
	for _, role := range m.roleOrder {
		lr, err := m.LaunchRules(role)
		if err != nil {
			t.Fatal(err)
		}
		switch role {
		case "master", "docs":
			if !m.MayDeliver(role) || !slices.Contains(lr.Allow, "Bash(agentbus deliver *)") || HoldsDeliverDeny(lr) {
				t.Errorf("%s does not deliver: %+v", role, lr)
			}
		default:
			if m.MayDeliver(role) || !HoldsDeliverDeny(lr) {
				t.Errorf("%s may deliver: deny %v", role, lr.Deny)
			}
		}
	}
	// Sentinel reloads agents (lifecycle part C): that one command, nothing else.
	s, err := m.LaunchRules("sentinel")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Allow, []string{"Bash(agentbus reload *)"}) || len(s.Ask) != 0 {
		t.Fatalf("sentinel rules = %+v", s)
	}
}

// The example manifests follow the shipped rule (forge-delivery): only master
// and docs deliver, every other role carries both denies.
func TestExampleManifestsDeliverRules(t *testing.T) {
	base, err := os.ReadFile(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	add, err := os.ReadFile(repoPath(t, "examples/roles.specialists.toml"))
	if err != nil {
		t.Fatal(err)
	}
	// The specialists are appended to a manifest like this repository's.
	specialists := writeConfig(t, string(base)+"\n"+string(add))
	for file, path := range map[string]string{"examples/roles.mixed.toml": repoPath(t, "examples/roles.mixed.toml"),
		"roles.toml + examples/roles.specialists.toml": specialists} {
		m, err := Load(path)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, role := range m.roleOrder {
			lr, err := m.LaunchRules(role)
			if err != nil {
				t.Fatalf("%s %s: %v", file, role, err)
			}
			if delivers := role == "master" || role == "docs"; delivers == HoldsDeliverDeny(lr) || delivers != m.MayDeliver(role) {
				t.Errorf("%s: %s deny %v", file, role, lr.Deny)
			}
		}
	}
}

// The deliver denies catch the command and its spellings after the project's
// own global flag, rendered at launch with the project's name, and leave the
// word in a message alone, flag or not (foureyes on step 6): deliver is
// everyday prose on the bus, and agents write --project <p> as a rule.
func TestDeliverDenyMatchesTheCommandNotProse(t *testing.T) {
	m, err := Load(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.ProjectLaunchRules("coder", "p")
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{
		"agentbus deliver coder/x",
		"agentbus deliver coder/x --task t1",
		"agentbus --project p deliver b",
		"agentbus --project=p deliver b",
		"agentbus -p p deliver b",
		"agentbus --project p --host h deliver b",
		"agentbus --project=p --host=h deliver b",
		"agentbus -p p -H h deliver b",
	} {
		if decide(r, cmd) != "deny" {
			t.Errorf("not denied: %s", cmd)
		}
	}
	for _, cmd := range []string{
		`agentbus report coder "we deliver it next"`,
		`agentbus --project p report coder "we deliver it next"`,
		`agentbus --project=p report coder --to master "deliver step 5"`,
		`agentbus -p p verdict --subject s coder approve "ok to deliver"`,
		`agentbus -p p reply --ref 1-0 master the PR to deliver is ready`,
		`agentbus --project p plan link t1 --ref coder/deliver@abc`,
	} {
		if decide(r, cmd) == "deny" {
			t.Errorf("prose denied: %s", cmd)
		}
	}
	// master and docs deliver: no project form is derived for them.
	for _, role := range []string{"master", "docs"} {
		mr, err := m.ProjectLaunchRules(role, "p")
		if err != nil {
			t.Fatal(err)
		}
		if decide(mr, "agentbus --project p deliver b") == "deny" || decide(mr, "agentbus deliver b") == "deny" {
			t.Errorf("%s may not deliver: deny %v", role, mr.Deny)
		}
	}
}

// MayDeliver is the rule agentbus deliver reads itself, whatever the
// command's spelling (reviewer-codex; master: enforce it in the command): a
// role delivers only with delivers = true; a role without it is refused even
// with no deny in its rules, so a role added by hand never delivers by
// default; a name that is no role (a person) may.
func TestMayDeliver(t *testing.T) {
	m, err := Load(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for role, want := range map[string]bool{"master": true, "docs": true, "charlie": true, "anonymous": true,
		"coder": false, "foureyes": false, "reviewer-codex": false, "infra": false, "sentinel": false} {
		if got := m.MayDeliver(role); got != want {
			t.Errorf("MayDeliver(%s) = %v, want %v", role, got, want)
		}
	}
	bare, err := Load(writeConfig(t, "[roles.master]\nmodel = \"sonnet\"\npermission = \"acceptEdits\"\ntier = \"boot\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if bare.MayDeliver("master") {
		t.Error("a role without delivers = true may deliver")
	}
}

func TestDeliversField(t *testing.T) {
	head := "[roles.master]\nmodel = \"sonnet\"\npermission = \"acceptEdits\"\ntier = \"boot\"\n"
	for name, c := range map[string]struct{ body, want string }{
		"not a boolean": {head + "delivers = \"yes\"\n", "delivers must be a boolean"},
		"and its deny":  {head + "delivers = true\n[roles.master.tool_rules]\ndeny = [\"Bash(agentbus deliver *)\"]\n", "delivers = true but its tool_rules deny"},
	} {
		if _, err := Load(writeConfig(t, c.body)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}
