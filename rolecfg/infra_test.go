package rolecfg

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// infra-role (Bernard): the infra pop role is an inert draft
// (examples/drafts/infra, for `agentbus roles adopt infra --from
// examples/drafts`). Appended to this repository's manifest, as adopt appends
// it, it loads as a pop role popped only through the person, on a manual
// profile; its session may check, plan, diff and lint, asks before any apply
// or destroy, and never reads a secret.
func TestInfraDraft(t *testing.T) {
	base, err := os.ReadFile(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	add, err := os.ReadFile(repoPath(t, "examples/drafts/infra/roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(writeConfig(t, string(base)+"\n"+string(add)))
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Resolve("infra")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(m.RolesByTier("pop"), "infra") || p.Approval != "person" || p.Options["permission"] != "manual" {
		t.Fatalf("infra: pop %v, approval %q, permission %q", slices.Contains(m.RolesByTier("pop"), "infra"), p.Approval, p.Options["permission"])
	}
	if !slices.Contains(p.Skills, "agent-bus") {
		t.Errorf("infra skills %v lack agent-bus", p.Skills)
	}
	r, err := m.LaunchRules("infra")
	if err != nil {
		t.Fatal(err)
	}

	// Allowed without a prompt: syntax and lint only, which run no code the
	// repository or a provider chose (master's classes).
	readOnly := regexp.MustCompile(`^Bash\((terraform|tofu) (fmt -check|validate)\*\)$|^Bash\((tflint|yamllint)\*\)$|^Bash\(ansible-lint --offline\*\)$|^Bash\(ansible-playbook --syntax-check \*\)$`)
	for _, rule := range r.Allow {
		if !readOnly.MatchString(rule) {
			t.Errorf("allow %q is not a syntax or lint check", rule)
		}
	}

	// Concrete commands, by class, as the client decides them: deny, then
	// ask, then allow, else the manual profile prompts.
	for _, c := range []struct{ cmd, want string }{
		// syntax and lint: no prompt
		{"terraform fmt -check -recursive", "allow"}, {"tofu validate", "allow"},
		{"tflint", "allow"}, {"tflint --recursive", "allow"}, {"yamllint .", "allow"},
		{"ansible-lint --offline site.yml", "allow"}, {"ansible-playbook --syntax-check site.yml", "allow"},
		// evaluates providers, modules or data sources, touches a target, downloads: the person's ok
		{"terraform plan", "ask"}, {"tofu plan -var-file=prod.tfvars", "ask"},
		{"terraform init", "ask"}, {"tofu init -upgrade", "ask"}, {"terraform refresh", "ask"}, {"tofu refresh", "ask"}, {"terraform -chdir=env/prod plan", "ask"},
		{"tflint --init", "ask"}, {"tflint --recursive --init", "ask"},
		{"ansible-playbook --check site.yml", "ask"}, {"ansible-playbook -i prod --check site.yml", "ask"},
		{"ansible-playbook --diff site.yml", "ask"}, {"ansible-lint site.yml", "prompt"},
		// changes, state surgery included: the person's ok
		{"terraform apply", "ask"}, {"tofu destroy -target=x", "ask"}, {"terraform -chdir=env/prod apply", "ask"},
		{"terraform import a.b id", "ask"}, {"tofu taint a.b", "ask"},
		{"terraform state mv a b", "ask"}, {"tofu state rm a.b", "ask"}, {"terraform -chdir=x state push s", "ask"},
		{"ansible-playbook -i prod site.yml", "prompt"}, // a real run: the manual pane (an ask catch-all would also catch --syntax-check)
		// secrets: never
		{"terraform output -raw db_password", "deny"}, {"terraform -chdir=env/prod output", "deny"},
		{"terraform state pull", "deny"}, {"tofu -chdir=x state show a.b", "deny"},
		{"terraform show", "deny"}, {"tofu console", "deny"},
		{"terraform plan -out=prod.tfplan", "deny"}, {"tofu plan -lock=false -out x", "deny"},
		{"ansible-vault view secrets.yml", "deny"}, {"printenv", "deny"}, {"env", "deny"},
	} {
		if got := decide(r, c.cmd); got != c.want {
			t.Errorf("%q: %s, want %s", c.cmd, got, c.want)
		}
	}

	// An ask rule refuses bypassPermissions, so the prompt cannot be switched off.
	loose := strings.Replace(string(add), `profile = "claude-opus-manual"`, `profile = "claude-opus-bypass"`, 1) +
		"\n[profiles.claude-opus-bypass]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\n[profiles.claude-opus-bypass.options]\npermission = \"bypassPermissions\"\n"
	_, lerr := Load(writeConfig(t, string(base)+"\n"+loose))
	if lerr == nil {
		lm, _ := Load(writeConfig(t, string(base)+"\n"+loose))
		_, lerr = lm.Resolve("infra")
	}
	if lerr == nil || !strings.Contains(lerr.Error(), "bypassPermissions") {
		t.Errorf("infra on a bypassPermissions profile: %v, want a refusal naming bypassPermissions", lerr)
	}

	// Never a secret: state, plan files, vault passwords, cloud and cluster
	// credentials, plus the shared denies every role carries (~/.ssh).
	for _, rule := range []string{
		"Read(**/*.tfstate*)", "Read(**/.terraform/**)", "Read(**/*.tfplan)",
		"Read(**/*.tfvars*)", "Read(~/.terraform.d/**)",
		"Read(**/*vault_pass*)", "Read(**/.vault_pass*)", "Bash(ansible-vault *)",
		"Read(~/.aws/**)", "Read(~/.config/gcloud/**)", "Read(~/.azure/**)", "Read(~/.kube/**)",
		"Read(~/.ssh/**)",
	} {
		if !slices.Contains(r.Deny, rule) {
			t.Errorf("deny lacks %s", rule)
		}
	}

	// master pops infra only through the person.
	mr, err := m.LaunchRules("master")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(mr.Ask, "Bash(agent-spawn infra *)") || slices.Contains(mr.Allow, "Bash(agent-spawn infra *)") {
		t.Errorf("master pops infra without the person: allow %v ask %v", mr.Allow, mr.Ask)
	}

	// The draft is inert and adoptable: it defines [roles.infra] and nothing
	// else, and roles/infra.md does not exist yet (adopt never overwrites one).
	if strings.Count(string(add), "[profiles.") > 0 || !strings.Contains(string(add), "[roles.infra]") {
		t.Errorf("the draft's entry must define [roles.infra] and nothing else:\n%s", add)
	}
	if _, err := os.Stat(repoPath(t, "roles/infra.md")); err == nil {
		t.Error("roles/infra.md exists: roles adopt would refuse to install the draft's prompt")
	}
	if st, err := os.Stat(repoPath(t, "examples/drafts/infra/infra.md")); err != nil || st.Size() == 0 {
		t.Errorf("the draft's prompt: %v", err)
	}
}

// decide is the client's decision for a shell command under rules: the first
// list holding a matching Bash rule, deny before ask before allow; "prompt"
// when none matches (the manual profile asks). A rule's * matches anything.
func decide(r ToolRules, cmd string) string {
	match := func(rules []string) bool {
		for _, rule := range rules {
			body, ok := strings.CutPrefix(rule, "Bash(")
			if !ok {
				continue
			}
			parts := strings.Split(strings.TrimSuffix(body, ")"), "*")
			for i := range parts {
				parts[i] = regexp.QuoteMeta(parts[i])
			}
			if regexp.MustCompile("^" + strings.Join(parts, ".*") + "$").MatchString(cmd) {
				return true
			}
		}
		return false
	}
	switch {
	case match(r.Deny):
		return "deny"
	case match(r.Ask):
		return "ask"
	case match(r.Allow):
		return "allow"
	}
	return "prompt"
}
