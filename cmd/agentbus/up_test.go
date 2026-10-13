package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHerdr records the calls and answers like herdr's CLI.
type fakeHerdr struct {
	calls      []string
	workspaces string // JSON list of {"label","workspace_id"} entries
}

func (f *fakeHerdr) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch strings.Join(args[:2], " ") {
	case "workspace list":
		return []byte(`{"result":{"workspaces":[` + f.workspaces + `]}}`), nil
	case "workspace create":
		return []byte(`{"result":{"workspace":{"workspace_id":"w9"},"tab":{"tab_id":"w9:t1"},"root_pane":{"pane_id":"w9:p1"}}}`), nil
	case "tab create":
		n := len(f.calls)
		return []byte(`{"result":{"tab":{"tab_id":"w9:t` + string(rune('0'+n)) + `"},"root_pane":{"pane_id":"w9:p` + string(rune('0'+n)) + `"}}}`), nil
	}
	return []byte(`{"result":{}}`), nil
}

func upFixture(t *testing.T) (string, string, *fakeHerdr, *[]string, upDeps) {
	t.Helper()
	dir := t.TempDir()
	templates := filepath.Join(dir, "projects")
	work := filepath.Join(dir, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	h := &fakeHerdr{}
	var boot []string
	env := map[string]string{"HERDR_ENV": "1", "HERDR_PLUS_PROJECTS_DIR": templates}
	deps := upDeps{
		Herdr: h.run,
		Bootstrap: func(args []string) error {
			boot = append(boot, strings.Join(args, " "))
			if err := os.MkdirAll(templates, 0o755); err != nil {
				return err
			}
			body := "name = \"demo\"\nworking_dir = \"" + work + "\"\n\n[[tabs]]\nname = \"busmon\"\ncommand = \"/r/busmon --project demo\"\n\n[[tabs]]\nname = \"master\"\ncommand = \"env ROLES_TOML=/r/roles.toml /r/scripts/agent-launch master demo\"\n\n[[tabs]]\nname = \"dispatch\"\ncommand = \"/r/agentbus --project demo dispatch\"\n"
			return os.WriteFile(filepath.Join(templates, "demo.toml"), []byte(body), 0o644)
		},
		Env: func(k string) string { return env[k] },
	}
	return work, templates, h, &boot, deps
}

func TestUpProvisionsAndOpensBusmonFirst(t *testing.T) {
	work, _, h, boot, deps := upFixture(t)
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work, "--as", "bernard"}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	if len(*boot) != 1 || (*boot)[0] != "demo "+work+" --as bernard" {
		t.Fatalf("bootstrap calls = %q", *boot)
	}
	want := []string{
		"workspace list",
		"workspace create --cwd " + work + " --label demo --focus",
		"tab rename w9:t1 busmon",
		"tab create --workspace w9 --label master --no-focus",
		"tab create --workspace w9 --label dispatch --no-focus",
		"pane run w9:p1 /r/busmon --project demo",
	}
	got := strings.Join(h.calls, "\n")
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Fatalf("missing herdr call %q in:\n%s", w, got)
		}
	}
	if !strings.Contains(out.String(), "opened demo") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestUpFocusesAnOpenWorkspace(t *testing.T) {
	work, _, h, _, deps := upFixture(t)
	h.workspaces = `{"label":"demo","workspace_id":"w4"}`
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d: %s", code, errw.String())
	}
	got := strings.Join(h.calls, "\n")
	if !strings.Contains(got, "workspace focus w4") || strings.Contains(got, "workspace create") {
		t.Fatalf("a re-run must focus, not open a second workspace:\n%s", got)
	}
}

func TestUpOutsideHerdrProvisionsAndExplains(t *testing.T) {
	work, _, h, boot, deps := upFixture(t)
	deps.Env = func(k string) string { return "" }
	var out, errw bytes.Buffer
	if code := runUp([]string{"demo", work}, deps, &out, &errw); code != 0 {
		t.Fatalf("up = %d", code)
	}
	if len(*boot) != 1 || len(h.calls) != 0 {
		t.Fatalf("outside herdr: bootstrap %q, herdr %q", *boot, h.calls)
	}
	if !strings.Contains(out.String(), "start herdr") {
		t.Fatalf("no instructions: %q", out.String())
	}
}

func TestUpRefusesBadInput(t *testing.T) {
	_, _, _, _, deps := upFixture(t)
	var out, errw bytes.Buffer
	if code := runUp([]string{"Bad Name"}, deps, &out, &errw); code == 0 {
		t.Fatal("an invalid project name was accepted")
	}
	deps.Bootstrap = func([]string) error { return errors.New("broker down") }
	if code := runUp([]string{"demo"}, deps, &out, &errw); code == 0 || !strings.Contains(errw.String(), "broker down") {
		t.Fatalf("a bootstrap failure must stop up: %q", errw.String())
	}
}
