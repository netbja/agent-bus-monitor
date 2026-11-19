package modelcheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// UsedAt was the unexported usedAt in cmd/agentbus: roles doctor --json
// never printed it, and the exported field keeps it out (json:"-").
func TestDoctorRowJSONHasNoUsedAt(t *testing.T) {
	b, err := json.Marshal(DoctorRow{Agent: "coder", UsedAt: 1234})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(b)), "usedat") || strings.Contains(string(b), "1234") {
		t.Fatalf("UsedAt reached the JSON: %s", b)
	}
}

// RealDoctorDeps finds roles.toml through the getenv it is given
// (rolecfg.ManifestPath: ROLES_TOML first), as cmd/agentbus's manifestPath did
// with its environment.
func TestRealDoctorDepsReadsTheManifestFromGetenv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roles.toml")
	if err := os.WriteFile(path, []byte("schema_version = 2\n[profiles.p]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\noptions = { permission = \"manual\" }\n[defaults]\nprofile = \"p\"\n[roles.getenvprobe]\ntier = \"boot\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ROLES_TOML": path}
	none := func(string) string { return "" }
	d := RealDoctorDeps(func(k string) string { return env[k] }, none)
	m, err := d.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Roles["getenvprobe"]; !ok {
		t.Fatalf("not the manifest getenv named: roles %v", m.Roles)
	}
}
