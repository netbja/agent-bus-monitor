package rolecfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// examples/roles.local-llm.toml launches the local peer through its adapter
// script with the launch spec, and carries the shipped Read denies, so a
// throwaway project built from it never gets a peer that reads .env.
func TestLocalLLMExample(t *testing.T) {
	m, err := Load(repoPath(t, "examples/roles.local-llm.toml"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Resolve("local")
	if err != nil {
		t.Fatal(err)
	}
	if p.Client != "local-llm" || p.Provider != "local" || p.Model == "" || p.Options["endpoint"] != "local" {
		t.Fatalf("profile = %+v", p)
	}
	if p.Adapter != repoPath(t, "scripts/local-llm-peer") {
		t.Fatalf("adapter = %q", p.Adapter)
	}
	if fi, err := os.Stat(p.Adapter); err != nil || fi.Mode()&0o111 == 0 {
		t.Fatalf("the adapter script is not executable: %v", err)
	}
	rules, err := m.LaunchRules("local")
	if err != nil {
		t.Fatal(err)
	}
	shipped, err := Load(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, d := range shipped.SharedToolRules.Deny {
		if strings.HasPrefix(d, "Read(") {
			want = append(want, d)
		}
	}
	if strings.Join(rules.Deny, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the example's denies differ from the shipped Read denies:\n%v\nwant\n%v", rules.Deny, want)
	}
	if _, err := os.Stat(filepath.Join(repoPath(t, "roles"), "local.md")); err != nil {
		t.Fatal("roles/local.md is missing")
	}
}
