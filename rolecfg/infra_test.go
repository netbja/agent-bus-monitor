package rolecfg

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// infra-role (Charlie): the infra pop role, reviewed as a draft
// (examples/drafts/infra) and adopted into this repository's manifest with
// `agentbus roles adopt infra --from examples/drafts` (#187). It loads as a
// pop role popped only through the person, on a manual profile; its session
// may check, plan, diff and lint, asks before any apply or destroy, and never
// reads a secret.
func TestInfraRole(t *testing.T) {
	base, err := os.ReadFile(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	add, err := os.ReadFile(repoPath(t, "examples/drafts/infra/roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(repoPath(t, "roles.toml"))
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

	// Allowed without a prompt: exactly these invocations, no wildcard
	// (master), so no flag or argument rides on an allow.
	exact := []string{
		"Bash(terraform fmt -check)", "Bash(terraform fmt -check -recursive)", "Bash(terraform validate)",
		"Bash(tofu fmt -check)", "Bash(tofu fmt -check -recursive)", "Bash(tofu validate)",
		"Bash(tflint)", "Bash(tflint --recursive)", "Bash(ansible-lint --offline)", "Bash(yamllint .)",
	}
	if !slices.Equal(r.Allow, exact) {
		t.Errorf("allow %v, want exactly %v", r.Allow, exact)
	}
	for _, rule := range r.Allow {
		if strings.Contains(rule, "*") {
			t.Errorf("allow %q has a wildcard", rule)
		}
	}

	// Concrete commands, by class, as the client decides them: deny, then
	// ask, then allow, else the manual profile prompts.
	for _, c := range []struct{ cmd, want string }{
		// syntax and lint: no prompt
		{"terraform fmt -check -recursive", "allow"}, {"tofu validate", "allow"},
		{"tflint", "allow"}, {"tflint --recursive", "allow"}, {"yamllint .", "allow"},
		{"ansible-lint --offline", "allow"},
		// anything beyond the exact forms: the pane prompts (or an ask rule)
		{"terraform fmt -check=false", "prompt"}, {"tofu fmt -check=false", "prompt"}, // writes files
		{"terraform fmt", "prompt"}, {"terraform validate -json", "prompt"},
		{"ansible-lint --offline site.yml", "prompt"}, {"ansible-playbook --syntax-check site.yml", "ask"},
		{"yamllint -c x.yml .", "prompt"},
		// evaluates providers, modules or data sources, touches a target, downloads: the person's ok
		{"terraform plan", "ask"}, {"tofu plan -var-file=prod.tfvars", "ask"},
		{"terraform init", "ask"}, {"tofu init -upgrade", "ask"}, {"terraform refresh", "ask"}, {"tofu refresh", "ask"}, {"terraform -chdir=env/prod plan", "ask"},
		{"tflint --init", "ask"}, {"tflint --recursive --init", "ask"},
		{"ansible-playbook --check site.yml", "ask"}, {"ansible-playbook -i prod --check site.yml", "ask"},
		{"ansible-playbook --diff site.yml", "ask"}, {"ansible-lint site.yml", "prompt"},
		{"ansible-lint --offline --fix site.yml", "ask"}, {"ansible-lint --offline --write site.yml", "ask"},
		// changes, state surgery included: the person's ok
		{"terraform apply", "ask"}, {"tofu destroy -target=x", "ask"}, {"terraform -chdir=env/prod apply", "ask"},
		{"terraform import a.b id", "ask"}, {"tofu taint a.b", "ask"},
		{"terraform -chdir=prod import a.b id", "ask"}, {"tofu -chdir=prod taint a.b", "ask"},
		{"terraform -chdir=prod taint a.b", "ask"}, {"tofu -chdir=prod import a.b id", "ask"},
		{"terraform state mv a b", "ask"}, {"tofu state rm a.b", "ask"}, {"terraform -chdir=x state push s", "ask"},
		{"terraform test", "ask"}, {"tofu -chdir=x test", "ask"},
		{"terraform force-unlock 1234", "ask"}, {"tofu -chdir=x force-unlock 1234", "ask"},
		{"terraform get -update", "ask"}, {"tofu -chdir=x get", "ask"},
		{"ansible all -i prod -m shell -a reboot", "ask"}, {"ansible-pull -U https://x/repo.git", "ask"},
		{"ansible-galaxy install -r requirements.yml", "ask"}, {"ansible-galaxy collection install a.b", "ask"},
		{"ansible-playbook -i prod site.yml", "ask"}, {"ansible-playbook site.yml", "ask"}, // every playbook run is asked
		{"terraform workspace delete -force prod", "ask"}, {"tofu -chdir=x workspace select prod", "ask"},
		{"terraform workspace new -state=prod.tfstate staging", "deny"}, {"tofu workspace new -lock=false -state=x.tfstate y", "deny"},
		// secrets: never
		{"terraform output -raw db_password", "deny"}, {"terraform -chdir=env/prod output", "deny"},
		{"terraform state pull", "deny"}, {"tofu -chdir=x state show a.b", "deny"},
		{"terraform show", "deny"}, {"tofu console", "deny"},
		{"terraform plan -out=prod.tfplan", "deny"}, {"tofu plan -lock=false -out x", "deny"},
		{"terraform -chdir=env/prod plan -out=p.tfplan", "deny"}, {"tofu -chdir=x plan -lock=false -out=p", "deny"},
		{"ansible-vault view secrets.yml", "deny"}, {"printenv", "deny"}, {"env", "deny"}, {"env -0", "deny"}, {"env AWS_KEY=x", "deny"},
		{"env TF_VAR_x=1 terraform apply", "deny"},
	} {
		if got := decide(r, c.cmd); got != c.want {
			t.Errorf("%q: %s, want %s", c.cmd, got, c.want)
		}
	}

	// An ask rule refuses bypassPermissions, so the prompt cannot be switched off.
	loose := strings.Replace(string(add), `profile = "claude-opus-manual"`, `profile = "claude-opus-bypass"`, 1) +
		"\n[profiles.claude-opus-bypass]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\n[profiles.claude-opus-bypass.options]\npermission = \"bypassPermissions\"\n"
	looseManifest := strings.Replace(string(base), string(add), loose, 1)
	_, lerr := Load(writeConfig(t, looseManifest))
	if lerr == nil {
		lm, _ := Load(writeConfig(t, looseManifest))
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

	// What runs is what was reviewed: the manifest holds the draft's entry
	// byte for byte, once, and roles/infra.md is the draft's prompt. The draft
	// defines [roles.infra] and nothing else.
	if strings.Count(string(add), "[profiles.") > 0 || !strings.Contains(string(add), "[roles.infra]") {
		t.Errorf("the draft's entry must define [roles.infra] and nothing else:\n%s", add)
	}
	if strings.Count(string(base), string(add)) != 1 || strings.Count(string(base), "[roles.infra]") != 1 {
		t.Error("roles.toml does not hold the reviewed draft's entry exactly once")
	}
	prompt, err := os.ReadFile(repoPath(t, "roles/infra.md"))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := os.ReadFile(repoPath(t, "examples/drafts/infra/infra.md"))
	if err != nil || len(draft) == 0 || string(prompt) != string(draft) {
		t.Errorf("roles/infra.md is not the reviewed draft's prompt (%v)", err)
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
