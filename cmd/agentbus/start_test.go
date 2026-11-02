package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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

// A recorded name is never replaced by --as: it is used for this up only
// (foureyes).
func TestStartNeverReplacesARecordedName(t *testing.T) {
	w := newStartWorld(t)
	write(t, w.config, "[identity]\nas = \"alice\"\n")
	before := w.snapshot()
	if code := w.start(w.folder, "--yes", "--as", "bob"); code != 0 || w.snapshot() != before || !strings.Contains(strings.Join(w.ups[0], " "), "--as bob") {
		t.Fatalf("--as over a recorded name = %d, ups %q, or config.toml changed", code, w.ups)
	}
}

// The template as written decides a team change and the live guard: a stale
// profile is rewritten by --roles naming the role's own (reviewer-codex); a
// tab for a role roles.toml no longer has is a live agent all the same
// (reviewer-codex); an agent whose liveness cannot be proved counts as live
// (foureyes: macOS reads every agent unknown).
func TestStartJudgesTheTemplateAsWritten(t *testing.T) {
	w := newStartWorld(t)
	write(t, w.config, "[identity]\nas = \"charlie\"\n")
	w.writeTemplate("shop", []rolecfg.TeamRole{{Role: "master"}, {Role: "foureyes", Profile: "other"}})
	if code := w.start(w.folder, "--yes", "--roles", "master,foureyes"); code != 0 || !strings.Contains(strings.Join(w.ups[0], " "), "new --roles master,foureyes") {
		t.Fatalf("stale profile repaired = %d %q, ups %q", code, w.err.String(), w.ups)
	}

	w = newStartWorld(t)
	w.writeTemplate("shop", []rolecfg.TeamRole{{Role: "master"}, {Role: "ghost"}})
	w.live["ghost"] = lifeAlive
	if code := w.start(w.folder, "--yes", "--roles", "master"); code == 0 || !strings.Contains(w.err.String(), "agentbus retire ghost") || len(w.ups) != 0 {
		t.Fatalf("a live removed role dropped = %d %q", code, w.err.String())
	}

	w = newStartWorld(t)
	w.writeTemplate("shop", nil)
	w.live["coder"] = lifeUnknown
	if code := w.start(w.folder, "--yes", "--roles", "master,foureyes"); code == 0 || !strings.Contains(w.err.String(), "agentbus retire coder") {
		t.Fatalf("an unknown liveness dropped = %d %q", code, w.err.String())
	}
}

// outsideWorld fakes what start does outside herdr: herdr's launch, the
// session's socket as herdr session list reports it, and up in that session.
type outsideWorld struct {
	launched  []string // sessions herdr was started for
	socket    string   // "" = the session never listed running
	inSession [][]string
	inSock    string
	upCode    int
	herdrExit error
	order     []string
	tty       io.ReadWriter // what herdr was given
	release   chan struct{} // up waits until herdr exits
	mu        sync.Mutex    // up runs beside herdr's wait

	// start-tabs: the session's first workspace.
	running bool     // the session ran before start launched herdr
	runErr  error    // SessionRunning's error: unknown whether it ran
	initial string   // what InitialWorkspace finds ("" = none)
	asked   int      // InitialWorkspace calls
	ignored []string // the workspace each up was told to ignore
	closed  []string // CloseInitial calls
}

func (o *outsideWorld) note(s string) { o.mu.Lock(); defer o.mu.Unlock(); o.order = append(o.order, s) }

func (w *startWorld) outside(o *outsideWorld) startDeps {
	d := w.deps()
	d.Up = func(args []string) int { w.ups = append(w.ups, args); o.note("provision"); return 0 }
	d.HerdrOnPath = func() bool { return true }
	d.Herdr = func(session string, tty io.ReadWriter) (func() error, error) {
		o.launched, o.tty = append(o.launched, session), tty
		o.note("herdr")
		return func() error {
			o.note("herdr exit")
			if o.release != nil {
				close(o.release) // herdr gone while up still runs
			}
			return o.herdrExit
		}, nil
	}
	d.SessionSocket = func(session string) (string, error) {
		if o.socket == "" {
			return "", errors.New("herdr session " + session + " did not start")
		}
		return o.socket, nil
	}
	d.SessionRunning = func(session string) (bool, error) { return o.running, o.runErr }
	d.InitialWorkspace = func(socket string) (string, error) {
		o.mu.Lock()
		o.asked++
		o.mu.Unlock()
		return o.initial, nil
	}
	d.CloseInitial = func(socket, ws string) (string, error) {
		o.mu.Lock()
		o.closed = append(o.closed, ws)
		o.mu.Unlock()
		o.note("close " + ws)
		return "closed herdr's first workspace " + ws, nil
	}
	d.UpInSession = func(args []string, session, socket, ignore string, log io.Writer) int {
		if o.release != nil {
			<-o.release
		}
		o.mu.Lock()
		o.inSession, o.inSock = append(o.inSession, args), socket
		o.ignored = append(o.ignored, ignore)
		o.mu.Unlock()
		o.note("up in " + session)
		fmt.Fprintln(log, "opened")
		return o.upCode
	}
	d.LogPath = func(project string) (string, error) { return filepath.Join(w.t.TempDir(), project, "start.log"), nil }
	return d
}

// Outside herdr with a terminal (agentbus-start spec, "Outside herdr"):
// provision as up does, launch herdr --session <project> on the terminal,
// read the session's socket, run up inside it (its output to start.log),
// then return herdr's status; without a terminal, only up (its hint).
func TestStartOutsideHerdr(t *testing.T) {
	w := newStartWorld(t)
	o := &outsideWorld{socket: "/xdg/herdr/sessions/shop/herdr.sock"}
	d := w.outside(o)
	d.TTY = func() io.ReadWriter { return &fakeTTY{Reader: strings.NewReader("")} }
	if code := runStart([]string{w.folder, "--yes"}, d); code != 0 {
		t.Fatalf("start = %d: %s", code, w.err.String())
	}
	if !strings.Contains(w.out.String(), "starting herdr --session shop now") {
		t.Fatalf("not said: %q", w.out.String())
	}
	if !strings.HasPrefix(strings.Join(o.order, ","), "provision,herdr,") || len(o.launched) != 1 || o.launched[0] != "shop" || o.inSock != o.socket {
		t.Fatalf("order %q, launched %q, socket %q", o.order, o.launched, o.inSock)
	}
	if strings.Join(o.inSession[0], " ") != strings.Join(w.ups[0], " ") {
		t.Fatalf("up in the session %q, provision %q", o.inSession, w.ups)
	}

	// The session never listed: herdr keeps the terminal; after it, the
	// reason and the command to run inside it; exit 1.
	w = newStartWorld(t)
	o = &outsideWorld{}
	d = w.outside(o)
	d.TTY = func() io.ReadWriter { return &fakeTTY{Reader: strings.NewReader("")} }
	if code := runStart([]string{w.folder, "--yes"}, d); code != 1 || !strings.Contains(w.err.String(), "did not start") || !strings.Contains(w.err.String(), "agentbus up shop") || len(o.inSession) != 0 {
		t.Fatalf("no session = %d %q", code, w.err.String())
	}

	// up failing inside the session: after herdr exits, the log is named.
	w = newStartWorld(t)
	o = &outsideWorld{socket: "/s.sock", upCode: 1}
	d = w.outside(o)
	d.TTY = func() io.ReadWriter { return &fakeTTY{Reader: strings.NewReader("")} }
	if code := runStart([]string{w.folder, "--yes"}, d); code != 1 || !strings.Contains(w.err.String(), "start.log") {
		t.Fatalf("up failed in the session = %d %q", code, w.err.String())
	}

	// No terminal: provision only, nothing launched.
	w = newStartWorld(t)
	o = &outsideWorld{socket: "/s.sock"}
	if code := runStart([]string{w.folder, "--yes"}, w.outside(o)); code != 0 || len(o.launched) != 0 || len(w.ups) != 1 {
		t.Fatalf("no terminal = %d, launched %q", code, o.launched)
	}

	// herdr not on PATH: said before anything is written.
	w = newStartWorld(t)
	before := w.snapshot()
	d = w.outside(&outsideWorld{})
	d.HerdrOnPath = func() bool { return false }
	if code := runStart([]string{w.folder, "--yes"}, d); code != 1 || !strings.Contains(w.err.String(), "https://herdr.dev") || w.snapshot() != before || len(w.ups) != 0 {
		t.Fatalf("no herdr = %d %q", code, w.err.String())
	}
}

// A refused project name is never kept (foureyes); choosing an existing
// project takes its template's team (reviewer-codex); master missing from a
// template is added in the assistant, and starting without it is refused
// (reviewer-codex); a team holding a role the toggles cannot keep is left as
// it was and the reason said (foureyes).
func TestStartAssistantProjectAndTeam(t *testing.T) {
	w := newStartWorld(t)
	other := t.TempDir()
	write(t, filepath.Join(w.projects, "taken.toml"), "name = \"taken\"\nworking_dir = \""+other+"\"\n")
	if code, screen := w.ask("1\ntaken\n\n\n"); code != 0 || len(w.ups) != 1 || w.ups[0][0] != "shop" || !strings.Contains(screen, "not another folder's project") {
		t.Fatalf("a refused name kept = %d %q\n%s", code, w.ups, screen)
	}

	w = newStartWorld(t)
	w.writeTemplate("shop", []rolecfg.TeamRole{{Role: "master"}, {Role: "foureyes"}})
	write(t, filepath.Join(w.projects, "shop-b.toml"), "name = \"shop-b\"\nworking_dir = \""+w.folder+"\"\n")
	if code, _ := w.ask("1\nshop\n\n"); code != 0 || strings.Join(w.ups[0], " ") != "shop "+w.folder+" --as root-admin" {
		t.Fatalf("choosing shop = %d %q", code, w.ups)
	}

	w = newStartWorld(t)
	w.writeTemplate("shop", []rolecfg.TeamRole{{Role: "coder"}})
	if code, screen := w.ask("\n4\n1\n\n\n"); code != 0 || !strings.Contains(screen, "master is required") || !strings.Contains(strings.Join(w.ups[0], " "), "new --roles master,coder") {
		t.Fatalf("master added = %d %q\n%s", code, w.ups, screen)
	}

	w = newStartWorld(t)
	w.writeTemplate("shop", []rolecfg.TeamRole{{Role: "master"}, {Role: "docs"}})
	if _, screen := w.ask("4\n3\n\nq\n"); !strings.Contains(screen, "docs is not a boot role") || len(w.ups) != 0 {
		t.Fatalf("a team the toggles cannot keep:\n%s", screen)
	}
}

// herdr gets the very terminal start opened, never start's own stdio, which
// may be redirected (reviewer-codex); herdr leaving while up still opens the
// team is said, with the log (foureyes); a state directory that cannot be
// told stops start before herdr (foureyes).
func TestStartOutsideHerdrTerminalAndTiming(t *testing.T) {
	w := newStartWorld(t)
	o := &outsideWorld{socket: "/s.sock", release: make(chan struct{})}
	d := w.outside(o)
	tty := &fakeTTY{Reader: strings.NewReader("")}
	d.TTY = func() io.ReadWriter { return tty }
	if code := runStart([]string{w.folder, "--yes"}, d); code != 0 {
		t.Fatalf("start = %d: %s", code, w.err.String())
	}
	if o.tty != tty {
		t.Fatalf("herdr was given %v, not the terminal start opened", o.tty)
	}
	if !strings.Contains(w.err.String(), "still opening the team") || !strings.Contains(w.err.String(), "start.log") {
		t.Fatalf("herdr left before up: %q", w.err.String())
	}

	w = newStartWorld(t)
	o = &outsideWorld{socket: "/s.sock"}
	d = w.outside(o)
	d.TTY = func() io.ReadWriter { return &fakeTTY{Reader: strings.NewReader("")} }
	d.LogPath = func(string) (string, error) { return "", errors.New("no state directory") }
	if code := runStart([]string{w.folder, "--yes"}, d); code != 1 || len(o.launched) != 0 || !strings.Contains(w.err.String(), "no state directory") {
		t.Fatalf("no log path = %d, launched %q, %q", code, o.launched, w.err.String())
	}
}

// closingTTY counts its closes.
type closingTTY struct {
	fakeTTY
	closed int
}

func (c *closingTTY) Close() error { c.closed++; return nil }

// The terminal is opened once: the assistant's handle is the one herdr gets,
// closed after it; a run that never reaches herdr closes it too (foureyes).
func TestStartOpensTheTerminalOnce(t *testing.T) {
	w := newStartWorld(t)
	o := &outsideWorld{socket: "/s.sock"}
	d := w.outside(o)
	opens, tty := 0, &closingTTY{fakeTTY: fakeTTY{Reader: strings.NewReader("\n")}}
	d.TTY = func() io.ReadWriter { opens++; return tty }
	if code := runStart([]string{w.folder}, d); code != 0 {
		t.Fatalf("start = %d: %s", code, w.err.String())
	}
	if opens != 1 || o.tty != io.ReadWriter(tty) || tty.closed != 1 {
		t.Fatalf("opened %d, herdr given the assistant's handle %v, closed %d", opens, o.tty == io.ReadWriter(tty), tty.closed)
	}

	// Inside herdr: the assistant's handle is closed once up is run.
	w = newStartWorld(t)
	w.env["HERDR_ENV"] = "1"
	d = w.outside(&outsideWorld{})
	tty = &closingTTY{fakeTTY: fakeTTY{Reader: strings.NewReader("\n")}}
	d.TTY = func() io.ReadWriter { return tty }
	if code := runStart([]string{w.folder}, d); code != 0 || tty.closed != 1 {
		t.Fatalf("inside herdr = %d, closed %d", code, tty.closed)
	}
}

// start-tabs: in a session start created, herdr's first workspace (one bare
// shell tab, labelled after the folder) is not the project's: up ignores it,
// so the project's own workspace opens busmon first, and once the team is
// open start closes it, when still untouched. A session that already ran
// has no such workspace; a failed up keeps it.
func TestStartClosesHerdrsFirstWorkspace(t *testing.T) {
	run := func(o *outsideWorld) *startWorld {
		t.Helper()
		w := newStartWorld(t)
		d := w.outside(o)
		d.TTY = func() io.ReadWriter { return &fakeTTY{Reader: strings.NewReader("")} }
		runStart([]string{w.folder, "--yes"}, d)
		return w
	}
	o := &outsideWorld{socket: "/s.sock", initial: "w1"}
	run(o)
	if len(o.ignored) != 1 || o.ignored[0] != "w1" || len(o.closed) != 1 || o.closed[0] != "w1" {
		t.Fatalf("new session: ignored %q, closed %q", o.ignored, o.closed)
	}
	if order := strings.Join(o.order, ","); !strings.Contains(order, "up in shop,close w1") && !strings.Contains(order, "up in shop,herdr exit,close w1") {
		t.Fatalf("closed before the team was open: %s", order)
	}

	o = &outsideWorld{socket: "/s.sock", initial: "w1", running: true}
	run(o)
	if o.asked != 0 || len(o.ignored) != 1 || o.ignored[0] != "" || len(o.closed) != 0 {
		t.Fatalf("a running session: asked %d, ignored %q, closed %q", o.asked, o.ignored, o.closed)
	}

	// Unknown whether the session ran: its first workspace may be the
	// person's, so it is neither looked for nor closed.
	o = &outsideWorld{socket: "/s.sock", initial: "w1", runErr: errors.New("herdr session list: timeout")}
	run(o)
	if o.asked != 0 || len(o.ignored) != 1 || o.ignored[0] != "" || len(o.closed) != 0 {
		t.Fatalf("an unread session: asked %d, ignored %q, closed %q", o.asked, o.ignored, o.closed)
	}

	o = &outsideWorld{socket: "/s.sock", initial: "w1", upCode: 1}
	run(o)
	if len(o.closed) != 0 {
		t.Fatalf("closed after a failed up: %q", o.closed)
	}
}
