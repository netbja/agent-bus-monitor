package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// startWorld: a folder in Git, the deduction fixture's manifest, projects
// and config dirs, an up that records its arguments, a liveness answer.
type startWorld struct {
	t        *testing.T
	m        *rolecfg.Manifest
	manifest string
	folder   string
	projects string
	config   string // config.toml's path
	env      map[string]string
	ups      [][]string
	live     map[string]life
	out, err bytes.Buffer
}

func newStartWorld(t *testing.T) *startWorld {
	t.Helper()
	m, projects, xdg, env := deduceFixture(t)
	folder := filepath.Join(t.TempDir(), "shop")
	if out, err := exec.Command("git", "init", "-q", folder).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	folder, _ = filepath.EvalSymlinks(folder)
	w := &startWorld{t: t, m: m, folder: folder, projects: projects, config: filepath.Join(xdg, "agentbus", "config.toml"), env: env, live: map[string]life{}}
	w.manifest = filepath.Join(filepath.Dir(projects), "roles.toml")
	return w
}

func (w *startWorld) deps() startDeps {
	return startDeps{
		Env:      func(k string) string { return w.env[k] },
		Manifest: func() (*rolecfg.Manifest, error) { return w.m, nil },
		Up:       func(args []string) int { w.ups = append(w.ups, args); return 0 },
		Live:     func(string) (map[string]life, error) { return w.live, nil },
		Repo:     func() (string, error) { return "/r", nil },
		Out:      &w.out, Err: &w.err,
	}
}

func (w *startWorld) start(args ...string) int {
	w.out.Reset()
	w.err.Reset()
	return runStart(args, w.deps())
}

// snapshot reads the config and projects dirs whole: a run that writes
// nothing leaves them byte-identical.
func (w *startWorld) snapshot() string {
	var b strings.Builder
	for _, dir := range []string{filepath.Dir(w.config), w.projects} {
		_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				data, _ := os.ReadFile(p)
				b.WriteString(p + "\n" + string(data) + "\n")
			}
			return nil
		})
	}
	return b.String()
}

// writeTemplate writes the project's template as bootstrap would for team.
func (w *startWorld) writeTemplate(project string, team []rolecfg.TeamRole) string {
	path := filepath.Join(w.projects, project+".toml")
	body := rolecfg.TemplateTeam(w.m, w.manifest, project, w.folder, "/r/scripts", "/r", "charlie", false, false, team)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		w.t.Fatal(err)
	}
	return path
}

// A new project with --yes: the deduced values, the name recorded (none
// was), no start.team (the team is the complete one), up on the folder.
func TestStartNewProjectYes(t *testing.T) {
	w := newStartWorld(t)
	if code := w.start(w.folder, "--yes"); code != 0 {
		t.Fatalf("start = %d: %s", code, w.err.String())
	}
	if len(w.ups) != 1 || strings.Join(w.ups[0], " ") != "shop "+w.folder+" --as root-admin" {
		t.Fatalf("up = %q", w.ups)
	}
	data, _ := os.ReadFile(w.config)
	if !strings.Contains(string(data), `as = "root-admin"`) || strings.Contains(string(data), "team") {
		t.Fatalf("config.toml:\n%s", data)
	}
}

// --roles: the team goes to bootstrap through up, and becomes the default
// for new projects; the complete team again removes that default.
func TestStartRolesWritesTheDefaultTeam(t *testing.T) {
	w := newStartWorld(t)
	if code := w.start(w.folder, "--yes", "--roles", "master,foureyes:review"); code != 0 {
		t.Fatalf("start = %d: %s", code, w.err.String())
	}
	if got := strings.Join(w.ups[0], " "); !strings.Contains(got, "--roles master,foureyes:review") {
		t.Fatalf("up = %q", got)
	}
	data, _ := os.ReadFile(w.config)
	if !strings.Contains(string(data), `team = "master,foureyes:review"`) {
		t.Fatalf("config.toml:\n%s", data)
	}
	if code := w.start(w.folder, "--yes", "--roles", "master,foureyes,coder"); code != 0 {
		t.Fatalf("start = %d: %s", code, w.err.String())
	}
	if data, _ := os.ReadFile(w.config); strings.Contains(string(data), "team =") {
		t.Fatalf("the complete team did not remove the default:\n%s", data)
	}
	// master cannot be removed.
	if code := w.start(w.folder, "--yes", "--roles", "coder"); code == 0 || !strings.Contains(w.err.String(), "master cannot be removed") {
		t.Fatalf("no master = %d %q", code, w.err.String())
	}
}

// A second start on a project whose template has the same team writes
// nothing of its own and passes no team: up recalls the template.
func TestStartIsIdempotent(t *testing.T) {
	w := newStartWorld(t)
	write(t, w.config, "[identity]\nas = \"charlie\"\n")
	w.writeTemplate("shop", nil)
	before := w.snapshot()
	if code := w.start(w.folder, "--yes"); code != 0 {
		t.Fatalf("start = %d: %s", code, w.err.String())
	}
	if strings.Join(w.ups[0], " ") != "shop "+w.folder+" --as charlie" || w.snapshot() != before {
		t.Fatalf("up = %q, or a file changed", w.ups)
	}
}

// A team change on an existing project rewrites its template through
// bootstrap new; a hand-edited one is refused, naming the file; dropping or
// moving a live role is refused, pointing at agentbus retire.
func TestStartTeamChangeOnAnExistingProject(t *testing.T) {
	w := newStartWorld(t)
	write(t, w.config, "[identity]\nas = \"charlie\"\n")
	path := w.writeTemplate("shop", nil)
	if code := w.start(w.folder, "--yes", "--roles", "master,foureyes"); code != 0 {
		t.Fatalf("start = %d: %s", code, w.err.String())
	}
	if got := strings.Join(w.ups[0], " "); got != "shop "+w.folder+" --as charlie new --roles master,foureyes" {
		t.Fatalf("up = %q", got)
	}

	w = newStartWorld(t)
	path = w.writeTemplate("shop", nil)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("\n[[tabs]]\nname = \"logs\"\ncommand = \"tail -f x\"\n")
	f.Close()
	before := w.snapshot()
	if code := w.start(w.folder, "--yes", "--roles", "master,foureyes"); code == 0 || !strings.Contains(w.err.String(), path+" was edited by hand") || len(w.ups) != 0 || w.snapshot() != before {
		t.Fatalf("hand-edited = %d %q, ups %q", code, w.err.String(), w.ups)
	}

	w = newStartWorld(t)
	w.writeTemplate("shop", nil)
	w.live["coder"] = lifeAlive
	if code := w.start(w.folder, "--yes", "--roles", "master,foureyes"); code == 0 || !strings.Contains(w.err.String(), "agentbus retire coder") || len(w.ups) != 0 {
		t.Fatalf("dropping a live role = %d %q", code, w.err.String())
	}
	w.live = map[string]life{"foureyes": lifeAlive}
	if code := w.start(w.folder, "--yes", "--roles", "master,foureyes:review,coder"); code == 0 || !strings.Contains(w.err.String(), "agentbus retire foureyes") {
		t.Fatalf("a live role's client changed = %d %q", code, w.err.String())
	}
}

// Without --yes and no terminal, start prints what it deduced and how to
// take it, writes nothing and exits 2; --dry-run writes nothing and runs no
// up; an unsettled value with --yes is refused with its flag.
func TestStartWithoutConsent(t *testing.T) {
	w := newStartWorld(t)
	before := w.snapshot()
	if code := w.start(w.folder); code != 2 || !strings.Contains(w.out.String(), "--yes") || !strings.Contains(w.out.String(), "shop") || len(w.ups) != 0 || w.snapshot() != before {
		t.Fatalf("no consent = %d %q", code, w.out.String())
	}
	if code := w.start(w.folder, "--yes", "--dry-run"); code != 0 || len(w.ups) != 0 || w.snapshot() != before {
		t.Fatalf("dry run = %d, ups %q", code, w.ups)
	}
	digits := filepath.Join(t.TempDir(), "2048")
	if err := os.MkdirAll(digits, 0o755); err != nil {
		t.Fatal(err)
	}
	if code := w.start(digits, "--yes"); code == 0 || !strings.Contains(w.err.String(), "--name") {
		t.Fatalf("unsettled project = %d %q", code, w.err.String())
	}
	if code := w.start(digits, "--yes", "--name", "game"); code != 0 || !strings.Contains(strings.Join(w.ups[len(w.ups)-1], " "), "game ") {
		t.Fatalf("--name = %d %q %q", code, w.err.String(), w.ups)
	}
}

// fakeTTY is the person's terminal: what they type, what the screen shows.
type fakeTTY struct {
	io.Reader
	screen bytes.Buffer
}

func (f *fakeTTY) Write(p []byte) (int, error) { return f.screen.Write(p) }

// ask runs start on a terminal where the person types typed.
func (w *startWorld) ask(typed string, absent ...string) (int, string) {
	tty := &fakeTTY{Reader: strings.NewReader(typed)}
	d := w.deps()
	d.TTY = func() io.ReadWriter { return tty }
	d.Look = func(exe string) (string, error) {
		if slices.Contains(absent, exe) {
			return "", errCLIAbsent
		}
		return "/bin/" + exe, nil
	}
	w.out.Reset()
	w.err.Reset()
	code := runStart([]string{w.folder}, d)
	return code, tty.screen.String()
}

// The assistant (agentbus-start spec): Enter starts with the deduced values;
// a line is changed by its number, checked by the same rule; master stays;
// a role's client is chosen among its own profile and fallbacks, one not
// installed never; q and the end of input write nothing.
func TestStartAssistant(t *testing.T) {
	w := newStartWorld(t)
	if code, screen := w.ask("\n"); code != 0 || len(w.ups) != 1 || !strings.Contains(screen, "Enter: start the team") {
		t.Fatalf("Enter = %d, ups %q\n%s", code, w.ups, screen)
	}

	w = newStartWorld(t)
	code, screen := w.ask("1\nShop!\nshop-api\n\n")
	if code != 0 || !strings.Contains(screen, "a lowercase letter") || w.ups[0][0] != "shop-api" {
		t.Fatalf("project = %d %q\n%s", code, w.ups, screen)
	}

	w = newStartWorld(t)
	code, screen = w.ask("4\n1\n3\nc2\n2\n\n\n")
	if code != 0 || !strings.Contains(screen, "master cannot be removed") || !strings.Contains(screen, "This team becomes the default") {
		t.Fatalf("team = %d\n%s", code, screen)
	}
	if got := strings.Join(w.ups[0], " "); !strings.Contains(got, "--roles master,foureyes:review") {
		t.Fatalf("up = %q", got)
	}
	if data, _ := os.ReadFile(w.config); !strings.Contains(string(data), `team = "master,foureyes:review"`) {
		t.Fatalf("start.team:\n%s", data)
	}

	w = newStartWorld(t)
	if _, screen := w.ask("4\nc1\n\nq\n"); !strings.Contains(screen, "no other client configured for master") {
		t.Fatalf("no fallback:\n%s", screen)
	}
	w = newStartWorld(t)
	if _, screen := w.ask("4\nc2\n2\n\nq\n", "codex"); !strings.Contains(screen, "not one to choose") || len(w.ups) != 0 {
		t.Fatalf("an absent client chosen:\n%s", screen)
	}

	for _, typed := range []string{"q\n", "4\n3\n", ""} {
		w = newStartWorld(t)
		before := w.snapshot()
		if code, _ := w.ask(typed); code != 0 || len(w.ups) != 0 || w.snapshot() != before {
			t.Fatalf("quit %q = %d, ups %q, or a file changed", typed, code, w.ups)
		}
	}

	w = newStartWorld(t)
	if _, screen := w.ask("\nq\n", "claude", "codex"); !strings.Contains(screen, "no agent CLI of this team is installed") || !strings.Contains(screen, "claude ✗ not found: https://code.claude.com/docs/en/setup") || len(w.ups) != 0 {
		t.Fatalf("no client:\n%s", screen)
	}
}
