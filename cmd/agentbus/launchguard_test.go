package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
)

// No test of this package reads a broker to launch a role: the resume
// records come from this stub unless a test sets its own.
func init() {
	launchResumes = func(string) (map[string]bus.ResumeRecord, error) { return nil, nil }
}

// guardFixture is a manifest of master and coder on Claude Code (whose
// resume is verified), in dry run: the launch prints its plan, runs nothing.
func guardFixture(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()
	manifest := filepath.Join(dir, "roles.toml")
	body := "schema_version = 2\n[defaults]\nprofile = \"main\"\n[profiles.main]\nclient = \"claude-code\"\nprovider = \"anthropic\"\nmodel = \"opus\"\noptions = { permission = \"manual\" }\n" +
		"[roles.master]\ntier = \"boot\"\n[roles.coder]\ntier = \"boot\"\n"
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "roles"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"master", "coder"} {
		if err := os.WriteFile(filepath.Join(dir, "roles", r+".md"), []byte(r+" prompt"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return []string{"ROLES_TOML=" + manifest, "AGENTBUS_REPO=" + dir, "AGENT_LAUNCH_DRYRUN=1"}
}

func withResumes(t *testing.T, recs map[string]bus.ResumeRecord, err error) {
	t.Helper()
	old := launchResumes
	t.Cleanup(func() { launchResumes = old })
	launchResumes = func(project string) (map[string]bus.ResumeRecord, error) {
		if project != "demo" {
			t.Errorf("records read for project %q", project)
		}
		return recs, err
	}
}

const (
	coderSess  = "11111111-2222-3333-4444-555555555555"
	masterSess = "66666666-7777-8888-9999-000000000000"
)

// The field failure (P1-7): the person followed the old hint and resumed
// coder's and foureyes' sessions as master; nothing refused it. A session
// recorded for another role resumes only as that role.
func TestLaunchRefusesASessionRecordedForAnotherRole(t *testing.T) {
	env := guardFixture(t)
	withResumes(t, map[string]bus.ResumeRecord{"coder": {Session: coderSess}, "master": {Session: masterSess}}, nil)
	code, out, errOut := roles(env, "launch", "master", "demo", "--resume", coderSess)
	if code == 0 || !strings.Contains(errOut, "recorded for coder") || strings.Contains(out, "claude") {
		t.Fatalf("master on coder's session: %d %q %q", code, out, errOut)
	}
	code, out, errOut = roles(env, "launch", "master", "demo", "--profile", "main", "--resume", coderSess)
	if code == 0 || !strings.Contains(errOut, "recorded for coder") {
		t.Fatalf("with --profile: %d %q %q", code, out, errOut)
	}
	if code, out, errOut := roles(env, "launch", "coder", "demo", "--resume", coderSess); code != 0 || !strings.Contains(out, "--resume "+coderSess) {
		t.Fatalf("coder on its own session: %d %q %q", code, out, errOut)
	}
	if code, out, errOut := roles(env, "launch", "master", "demo", "--resume", masterSess); code != 0 {
		t.Fatalf("master on its own session: %d %q %q", code, out, errOut)
	}
	// A session nobody recorded (one the person found by hand) is not refused.
	if code, _, errOut := roles(env, "launch", "master", "demo", "--resume", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"); code != 0 {
		t.Fatalf("an unrecorded session: %d %q", code, errOut)
	}
}

// A resume that cannot read the records is refused: it cannot tell whose
// session it is. A fresh launch never reads them.
func TestLaunchResumeWithoutTheRecordsIsRefused(t *testing.T) {
	env := guardFixture(t)
	withResumes(t, nil, errors.New("broker down"))
	if code, _, errOut := roles(env, "launch", "coder", "demo", "--resume", coderSess); code == 0 || !strings.Contains(errOut, "broker down") {
		t.Fatalf("resume, records unread: %d %q", code, errOut)
	}
	if code, _, errOut := roles(env, "launch", "coder", "demo"); code != 0 {
		t.Fatalf("a fresh launch: %d %q", code, errOut)
	}
}

// A launch inside an agent's environment (AGENT_BUS_AGENT, set by its own
// launch, and by the holder for the role it starts) starts only that role:
// agent-launch coder typed in master's terminal is refused.
func TestLaunchRefusesARoleThatIsNotTheCallers(t *testing.T) {
	env := guardFixture(t)
	code, out, errOut := roles(append(env, "AGENT_BUS_AGENT=master"), "launch", "coder", "demo")
	if code == 0 || !strings.Contains(errOut, "master") || !strings.Contains(errOut, "agentbus --project demo pop coder") || strings.Contains(out, "claude") {
		t.Fatalf("coder from master's terminal: %d %q %q", code, out, errOut)
	}
	if code, _, errOut := roles(append(env, "AGENT_BUS_AGENT=coder"), "launch", "coder", "demo"); code != 0 {
		t.Fatalf("coder in its own terminal: %d %q", code, errOut)
	}
	if code, _, errOut := roles(env, "launch", "coder", "demo"); code != 0 {
		t.Fatalf("outside any agent (the person's shell): %d %q", code, errOut)
	}
}
