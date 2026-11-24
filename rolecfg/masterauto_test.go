package rolecfg

import (
	"strings"
	"testing"
)

// The person (master-auto-default, then roles-auto-mode 2026-11-24): every
// Claude Code role starts in Claude Code's auto mode but security and infra,
// which stay manual (least privilege: auto would let the classifier approve a
// command no rule names). Master keeps its ask rules (Claude Code's auto mode
// still prompts on an explicit ask rule; deny rules block in every mode).
func TestClaudeRolesStartInAutoMode(t *testing.T) {
	m, err := Load(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	manual := map[string]bool{"security": true, "infra": true}
	claude := 0
	for _, role := range m.roleOrder {
		p, err := m.Resolve(role)
		if err != nil {
			t.Fatal(err)
		}
		if p.Client != "claude-code" {
			continue
		}
		claude++
		want := "auto"
		if manual[role] {
			want = "manual"
		}
		if p.Options["permission"] != want {
			t.Errorf("%s: permission %q, want %q", role, p.Options["permission"], want)
		}
	}
	if claude < 10 {
		t.Fatalf("only %d Claude Code roles resolved", claude)
	}
	if p, err := m.ResolveWith("architect", "claude-fable"); err != nil || p.Options["permission"] != "auto" {
		t.Errorf("architect on claude-fable: permission %q (%v), want auto", p.Options["permission"], err)
	}
	r, err := m.LaunchRules("master")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Ask) == 0 {
		t.Fatal("master lost its ask rules (the person's approval of deploy and infra pops)")
	}
	p, _ := m.Resolve("master")
	args, err := build(p, r, "p", "prompt", "/tmp/prompt", "", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i := range args {
		if args[i] == "--permission-mode" && i+1 < len(args) && args[i+1] == "auto" {
			found = true
		}
	}
	if !found {
		t.Fatalf("master's launch: %v", args)
	}
}

// The person (roles-auto-mode 2026-11-24): reviewer-codex runs on Luna, its
// sandbox read-only, its approval requests reviewed by Codex's own automatic
// review (approvals_reviewer, what --approve-for-me sets, without that flag's
// workspace-write sandbox), and its status line shows the tokens used.
func TestReviewerCodexRunsOnLunaWithAutoReview(t *testing.T) {
	m, err := Load(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Resolve("reviewer-codex")
	if err != nil {
		t.Fatal(err)
	}
	if p.Model != "gpt-6-luna" || p.Options["sandbox"] != "read-only" || p.Options["approval"] != "on-request" || p.Options["reviewer"] != "auto_review" {
		t.Fatalf("reviewer-codex: model %q, options %v", p.Model, p.Options)
	}
	args, err := build(p, ToolRules{}, "p", "prompt", "/tmp/prompt", "", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(args, " ")
	for _, want := range []string{"--sandbox read-only", `-c approvals_reviewer="auto_review"`, `"used-tokens"`} {
		if !strings.Contains(line, want) {
			t.Errorf("reviewer-codex's launch lacks %q: %s", want, line)
		}
	}
}
