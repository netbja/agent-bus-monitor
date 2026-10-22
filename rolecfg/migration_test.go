package rolecfg

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// The shipped manifest moved to schema 2 (specialist roles spec, part C):
// every role launches exactly as it did in schema 1 (testdata/roles.v1.toml
// is the last schema-1 manifest). Only the summary line a schema-2 dry run
// adds ("profile=… client=…") differs, and master's derived rules are equal.
func TestShippedManifestMigrationKeepsEveryLaunch(t *testing.T) {
	v1, err := Load(repoPath(t, "rolecfg/testdata/roles.v1.toml"))
	if err != nil {
		t.Fatal(err)
	}
	v2, err := Load(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if v2.SchemaVersion != 2 {
		t.Fatalf("roles.toml schema %d", v2.SchemaVersion)
	}
	if !reflect.DeepEqual(v1.roleOrder, v2.roleOrder) {
		t.Fatalf("roles %v, were %v", v2.roleOrder, v1.roleOrder)
	}
	env := []string{"AGENT_LAUNCH_DRYRUN=1", "AGENTBUS_REPO=" + repoPath(t, "."), "HOME=" + t.TempDir(), "PATH=/usr/bin:/bin"}
	dry := func(m *Manifest, role string) string {
		var out, errw bytes.Buffer
		if err := Launch(m, role, "demo", env, &out, &errw, nil); err != nil {
			t.Fatalf("%s: %v (%s)", role, err, errw.String())
		}
		var keep []string
		for _, l := range strings.Split(out.String(), "\n") {
			if !strings.HasPrefix(l, "profile=") {
				keep = append(keep, strings.ReplaceAll(l, repoPath(t, "rolecfg/testdata/roles.v1.toml"), "<manifest>"))
			}
		}
		return strings.ReplaceAll(strings.Join(keep, "\n"), repoPath(t, "roles.toml"), "<manifest>")
	}
	for _, role := range v1.roleOrder {
		if a, b := dry(v1, role), dry(v2, role); a != b {
			t.Errorf("%s launches differently:\nschema 1: %s\nschema 2: %s", role, a, b)
		}
	}
	r1, _ := v1.LaunchRules("master")
	r2, _ := v2.LaunchRules("master")
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("master's rules %+v, were %+v", r2, r1)
	}
}
