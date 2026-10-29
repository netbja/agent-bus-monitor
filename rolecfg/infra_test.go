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

	// Allowed without a prompt: only commands that change nothing.
	readOnly := regexp.MustCompile(`^Bash\((terraform|tofu) (fmt -check|validate|plan)\b|^Bash\(tflint\b|^Bash\(ansible-lint\b|^Bash\(ansible-playbook (--check|--syntax-check) `)
	for _, rule := range r.Allow {
		if !readOnly.MatchString(rule) {
			t.Errorf("allow %q is not a check, plan, diff or lint", rule)
		}
	}
	if len(r.Allow) == 0 {
		t.Error("infra allows nothing: check and plan would prompt every time")
	}
	for _, rule := range append(slices.Clone(r.Allow), r.Ask...) {
		for _, word := range []string{"apply", "destroy", "ansible-vault", " state", " output", " show", "import", "taint", "-auto-approve"} {
			if strings.Contains(rule, word) && slices.Contains(r.Allow, rule) {
				t.Errorf("allow %q: %q runs without the person", rule, strings.TrimSpace(word))
			}
		}
	}

	// apply and destroy prompt in the pane, whatever the profile becomes.
	for _, rule := range []string{"Bash(terraform apply*)", "Bash(terraform destroy*)", "Bash(tofu apply*)", "Bash(tofu destroy*)"} {
		if !slices.Contains(r.Ask, rule) {
			t.Errorf("ask lacks %s: %v", rule, r.Ask)
		}
	}
	// An ask rule refuses bypassPermissions, so the prompt cannot be switched off.
	loose := strings.Replace(string(add), `profile = "claude-opus-manual"`, `profile = "claude-opus-bypass"`, 1) +
		"\n[profiles.claude-opus-bypass]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\n[profiles.claude-opus-bypass.options]\npermission = \"bypassPermissions\"\n"
	if lm, err := Load(writeConfig(t, string(base)+"\n"+loose)); err == nil {
		if _, err := lm.Resolve("infra"); err == nil {
			t.Error("infra on a bypassPermissions profile resolves: its ask rules would never prompt")
		}
	}

	// Never a secret: state, plan files, vault passwords, cloud and cluster
	// credentials, plus the shared denies every role carries (~/.ssh).
	for _, rule := range []string{
		"Read(**/*.tfstate)", "Read(**/*.tfstate.backup)", "Read(**/.terraform/**)", "Read(**/*.tfplan)",
		"Read(**/*.tfvars)", "Read(~/.terraform.d/**)",
		"Read(**/*vault_pass*)", "Read(**/.vault_pass*)", "Bash(ansible-vault *)",
		"Read(~/.aws/**)", "Read(~/.config/gcloud/**)", "Read(~/.azure/**)", "Read(~/.kube/**)",
		"Bash(terraform state *)", "Bash(terraform output*)", "Bash(terraform show*)", "Bash(terraform console*)",
		"Bash(tofu state *)", "Bash(tofu output*)", "Bash(tofu show*)", "Bash(tofu console*)",
		"Bash(terraform plan * -out*)", "Bash(terraform plan -out*)", "Bash(tofu plan * -out*)", "Bash(tofu plan -out*)",
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
