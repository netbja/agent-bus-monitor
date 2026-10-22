package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRolesSkillUsers(t *testing.T) {
	repo, _ := filepath.Abs("../..")
	env := append(os.Environ(), "ROLES_TOML="+filepath.Join(repo, "roles.toml"))
	var out, errw bytes.Buffer
	if code := runRoles([]string{"skill-users", "agent-bus-sentinel", "tdd"}, env, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	if got := strings.TrimSpace(out.String()); got != "coder\nsentinel" {
		t.Fatalf("out %q", got)
	}
	out.Reset()
	if code := runRoles([]string{"skill-users"}, env, &out, &errw); code == 0 {
		t.Fatal("no skill accepted")
	}
}
