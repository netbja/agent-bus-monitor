package dispatchsvc

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The guard (dispatchsvc spec §6): the wall around the dispatch service's
// runtime, read from the package's non-test files. It fails on
//
//  1. an import of the CLI or the screen (cmd/..., bubbletea, lipgloss,
//     bubbles, tcell);
//  2. a use of mission beyond guardMissionReads: the package reads mission
//     records, it never runs a mission's tests. An allow list, not a deny
//     list: the runner side is wide (Run, RunTest, the Conductor's Pass,
//     Startup and HandleOp, Git's merges in a worktree), and a read added
//     here is reviewed;
//  3. os/exec imported by a file not in guardExecFiles;
//  4. a process started outside os/exec (guardStarters), or a program not
//     in guardPrograms: every exec.Command and exec.CommandContext names its
//     program as a string literal of the set, or is one of guardRunners,
//     whose parameter is the program and whose every call site in the package
//     names a literal of the set. A program the guard cannot read fails;
//  5. a shell (sh, bash, dash, zsh, env, by name or as a path ending in one),
//     whatever its arguments: never on the set, named so the failure says why.
//     `git -c core.fsmonitor=false -c core.pager=cat` (plandrift's gitSafe) is
//     Git configuration, not a shell, and passes;
//  6. a module import not in guardModuleImports.
//
// Rules 2 and 4 read qualified names (exec.Command, mission.HeldBy), so a dot
// import, which would hide them, fails as well.
//
// What it does not prove. It pins which module packages the runtime imports,
// not what they run: the packages that run programs themselves are marked in
// guardModuleImports with what each runs (the review's record). Adding a
// package to that list, or a program to what one runs, is reviewed against
// the rule "the dispatcher never runs repository code". Nor does it prove that
// git runs no repository code (hooks, core.fsmonitor): plandrift passes
// gitSafe and repocheck its own -c, the other git callers do not; that is the
// code as it was moved (spec §9). The acts injected through Deps (HostOps,
// PopEnd, Base) are CLI commands, reviewed as such in cmd/agentbus.

const guardModule = "github.com/netbja/agent-bus-monitor/"

// guardModuleImports: the module packages the runtime imports, on the day of
// the move. A comment names what a package runs itself (os/exec in its
// non-test files on that day).
var guardModuleImports = []string{
	"agentstate",
	"backup", // the after-hook (after.go), the secrets helper (secrets.go), the host's age lookup (host.go)
	"buildinfo",
	"bus",
	"config",
	"dispatch",
	"forge",
	"health",
	"history",
	"holder", // the holder and its terminals' programs (server.go, term.go, supervised.go)
	"internal/textfmt",
	"lifecycle",
	"loginexpiry", // the keychain reader (keychain.go)
	"mergequeue",  // git and the forge CLIs (forge.go, git.go, delivered.go)
	"mission",     // git (git.go) and RunTest (sh -c), the runner side: only guardMissionReads is used
	"modelcheck",
	"models", // the model readers (readers.go)
	"notify", // the desktop notifier (desktoprun.go)
	"peer",
	"plan",
	"quota", // a configured usage reader through sh -c (generic.go), the Claude and Codex probes (claudeprobe.go, codex.go, config.go)
	"relaunch",
	"rolecfg", // the launch and Grok hooks (launch.go, grokhooks.go)
	"selfrepair",
	"service", // systemctl / launchctl (manager.go, supervise.go)
	"termhost",
	"tokens",
	"usage",
}

// guardExecFiles: the files that import os/exec, on the day of the move.
var guardExecFiles = []string{
	"diskcheck.go", "dispatchconfig.go", "githubcheck.go", "healthchecks.go", "mergequeuechore.go",
	"notifychore.go", "plandrift.go", "repocheck.go", "selfrepairchore.go",
}

// guardPrograms: what the package runs itself, on the day of the move.
var guardPrograms = map[string]string{
	"git":    "the repository's state: merge queue, plan drift, repo and secrets checks, GitHub remotes",
	"docker": "the broker's container and docker's root (health, disk check)",
	"ssh":    "the GitHub check's authentication probe (ssh -T git@github.com, BatchMode)",
	"go":     "the disk check's GOCACHE (go env GOCACHE)",
}

// guardRunners: the functions whose program is a parameter. Their exec call
// is allowed on that parameter only; the guard checks their call sites
// instead (guardRunnerCalls).
var guardRunners = []struct{ file, fn, param, why string }{
	{"githubcheck.go", "runProbe", "name", "the GitHub check's probe runner; its call sites name ssh and git"},
	{"diskcheck.go", "realDiskDeps", "name", "the disk check's default Output; its call sites name docker and go"},
	{"healthchecks.go", "readVersion", "command", "the --version probe; no call site on the day of the move"},
}

// guardRunnerCalls: how a runner is called, and where its program is. A call
// matches by the called name: a direct call anywhere (fn), or a call through
// the field the runner is stored in, in the runner's file (sel).
var guardRunnerCalls = []struct {
	file, fn, sel string
	arg           int
}{
	{"", "runProbe", "", 2},
	{"githubcheck.go", "", "run", 2},
	{"", "realDiskDeps", "", -1}, // builds the deps, runs nothing
	{"diskcheck.go", "", "Output", 1},
	{"", "readVersion", "", 1},
}

// guardMissionReads: what the package uses of mission, on the day of the move.
var guardMissionReads = []string{"HeldBy"}

// guardStarters: the other ways to start a process; the package uses none.
var guardStarters = map[string][]string{
	"os":                    {"StartProcess"},
	"syscall":               {"Exec", "ForkExec", "StartProcess"},
	"golang.org/x/sys/unix": {"Exec"},
}

var guardShells = []string{"sh", "bash", "dash", "zsh", "env"}

func TestGuard(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	src := map[string]string{}
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		src[p] = string(b)
	}
	if len(src) < 30 {
		t.Fatalf("read %d files: the guard is not reading the package", len(src))
	}
	found, seen := guardFindings(t, src)
	for _, f := range found {
		t.Error(f)
	}
	// The lists are the day's: an entry nothing uses any more is stale.
	for _, p := range guardModuleImports {
		if !seen.imports[guardModule+p] {
			t.Errorf("guardModuleImports: %s is no longer imported, drop it", p)
		}
	}
	for _, f := range guardExecFiles {
		if !seen.execFiles[f] {
			t.Errorf("guardExecFiles: %s no longer imports os/exec, drop it", f)
		}
	}
	for _, m := range guardMissionReads {
		if !seen.missionReads[m] {
			t.Errorf("guardMissionReads: mission.%s is no longer used, drop it", m)
		}
	}
	for p := range guardPrograms {
		if !seen.programs[p] {
			t.Errorf("guardPrograms: %s is no longer run, drop it", p)
		}
	}
}

// The guard on fixtures: each way in is caught, and the package's own forms
// pass.
func TestGuardCases(t *testing.T) {
	const pkg = "package dispatchsvc\n"
	fail := map[string]map[string]string{
		"the CLI":  {"a.go": pkg + `import _ "github.com/netbja/agent-bus-monitor/cmd/agentbus"`},
		"a screen": {"a.go": pkg + `import _ "github.com/charmbracelet/bubbletea"`},
		"make": {"repocheck.go": pkg + `import "os/exec"
func f() { exec.Command("make").Run() }`},
		"a shell": {"repocheck.go": pkg + `import "os/exec"
func f() { exec.Command("sh", "-c", "true").Run() }`},
		"a shell by path": {"repocheck.go": pkg + `import x "os/exec"
func f(ctx context.Context) { x.CommandContext(ctx, "/usr/bin/bash", "-c", "true").Run() }`},
		"os/exec in a new file": {"new.go": pkg + `import "os/exec"
var _ = exec.LookPath`},
		"a module package not on the list": {"a.go": pkg + `import _ "github.com/netbja/agent-bus-monitor/internal/scrub"`},
		"the mission runner": {"a.go": pkg + `import m "github.com/netbja/agent-bus-monitor/mission"
var _ = m.RunTest`},
		"the mission conductor": {"a.go": pkg + `import "github.com/netbja/agent-bus-monitor/mission"
func f(ctx context.Context) { (&mission.Conductor{}).Pass(ctx) }`},
		"mission's git": {"a.go": pkg + `import "github.com/netbja/agent-bus-monitor/mission"
func f(ctx context.Context) { mission.Git{}.MergeNoFF(ctx, ".", "x", "m") }`},
		"exec.Command as a value": {"repocheck.go": pkg + `import "os/exec"
func f() { c := exec.Command; c("sh", "-c", "true").Run() }`},
		"exec.CommandContext as a value": {"repocheck.go": pkg + `import "os/exec"
var run = exec.CommandContext`},
		"os/exec dot-imported": {"repocheck.go": pkg + `import . "os/exec"
func f() { Command("make").Run() }`},
		"mission dot-imported": {"a.go": pkg + `import . "github.com/netbja/agent-bus-monitor/mission"
func f() { RunTest(nil, ".", "make", "", 0) }`},
		"os.StartProcess": {"a.go": pkg + `import "os"
func f() { os.StartProcess("/usr/bin/make", nil, nil) }`},
		"syscall.Exec": {"a.go": pkg + `import sc "syscall"
func f() { sc.Exec("/usr/bin/make", nil, nil) }`},
		"unix.Exec": {"a.go": pkg + `import "golang.org/x/sys/unix"
func f() { unix.Exec("/usr/bin/make", nil, nil) }`},
		"a program in a variable": {"repocheck.go": pkg + `import "os/exec"
func f(p string) { exec.Command(p).Run() }`},
		"a runner's parameter outside the runner": {"githubcheck.go": pkg + `import "os/exec"
func other(name string) { exec.Command(name).Run() }`},
		"a runner called with a variable":             {"a.go": pkg + `func f(p string) { readVersion(nil, p) }`},
		"a runner called with a program off the list": {"githubcheck.go": pkg + `func (c *githubCheck) f() { c.run(nil, nil, "curl", "x") }`},
		"a runner taken as a value elsewhere":         {"a.go": pkg + `var r = readVersion`},
		"an exec.Cmd built by hand": {"repocheck.go": pkg + `import "os/exec"
var c = &exec.Cmd{Path: "/bin/true"}`},
	}
	for name, src := range fail {
		if found, _ := guardFindings(t, src); len(found) == 0 {
			t.Errorf("%s: not caught", name)
		}
	}
	pass := map[string]string{
		"plandrift.go": pkg + `import ("context"; "os/exec")
var gitSafe = []string{"-c", "core.fsmonitor=false", "-c", "core.pager=cat"}
func gitRead(ctx context.Context, root string, args ...string) { exec.CommandContext(ctx, "git", args...).Run() }
func f(ctx context.Context) { gitRead(ctx, ".", append(gitSafe, "log")...) }`,
		"healthchecks.go": pkg + `import ("context"; "os/exec")
func f(ctx context.Context) { exec.CommandContext(ctx, "docker", "ps", "--filter", "publish=6380").Run() }
func readVersion(ctx context.Context, command string) { exec.CommandContext(ctx, command, "--version").Run() }`,
		"githubcheck.go": pkg + `import ("context"; "os/exec")
type githubCheck struct{ run func(context.Context, []string, string, ...string) }
func runProbe(ctx context.Context, env []string, name string, args ...string) { exec.CommandContext(ctx, name, args...).Run() }
func newGitHubCheck() *githubCheck { return &githubCheck{run: runProbe} }
func (c *githubCheck) f() { c.run(nil, nil, "ssh", "-T", "git@github.com"); c.run(nil, nil, "git", "ls-remote") }`,
		"a.go": pkg + `import "github.com/netbja/agent-bus-monitor/mission"
var _ = mission.HeldBy`,
	}
	found, _ := guardFindings(t, pass)
	for _, f := range found {
		t.Errorf("a form the package uses is refused: %s", f)
	}
}

type guardSeen struct{ imports, execFiles, programs, missionReads map[string]bool }

// guardFindings applies the guard's six rules to the sources (file name ->
// Go source).
func guardFindings(t *testing.T, src map[string]string) ([]string, guardSeen) {
	t.Helper()
	seen := guardSeen{map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}}
	var found []string
	fail := func(fset *token.FileSet, n ast.Node, format string, a ...any) {
		found = append(found, fset.Position(n.Pos()).String()+": "+fmt.Sprintf(format, a...))
	}
	names := make([]string, 0, len(src))
	for n := range src {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, src[name], 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		local := map[string]string{} // import path -> name in this file
		for _, im := range f.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			seen.imports[p] = true
			n := path.Base(p)
			if im.Name != nil {
				n = im.Name.Name
			}
			local[p] = n
			switch {
			case n == ".":
				fail(fset, im, "dot-imports %s: the guard reads os/exec and mission by their qualified names, so the package imports nothing with a dot", p)
			case strings.HasPrefix(p, guardModule+"cmd/"), guardScreen(p):
				fail(fset, im, "imports %s: the runtime never imports the CLI or the screen (rule 1)", p)
			case strings.HasPrefix(p, guardModule) && !slices.Contains(guardModuleImports, strings.TrimPrefix(p, guardModule)):
				fail(fset, im, "imports %s, a module package not in guardModuleImports (rule 6): what it runs is reviewed before it is added", p)
			case p == "os/exec" && !slices.Contains(guardExecFiles, name):
				fail(fset, im, "imports os/exec, and %s is not in guardExecFiles (rule 3)", name)
			}
			if p == "os/exec" {
				seen.execFiles[name] = true
			}
		}
		execName, missionName := local["os/exec"], local[guardModule+"mission"]
		starters := map[string][]string{} // name in this file -> the process starters guardStarters lists for it
		for p, fns := range guardStarters {
			if n, ok := local[p]; ok {
				starters[n] = fns
			}
		}
		calls := map[ast.Expr]bool{} // the called function of every call
		ast.Inspect(f, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				calls[c.Fun] = true
			}
			return true
		})
		for _, d := range f.Decls {
			fn := ""
			if fd, ok := d.(*ast.FuncDecl); ok {
				fn = fd.Name.Name
			}
			ast.Inspect(d, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SelectorExpr:
					if x, ok := n.X.(*ast.Ident); ok && missionName != "" && x.Name == missionName {
						if !slices.Contains(guardMissionReads, n.Sel.Name) {
							fail(fset, n, "uses mission.%s, not in guardMissionReads (rule 2): the runtime reads mission records, it never runs a mission", n.Sel.Name)
						}
						seen.missionReads[n.Sel.Name] = true
					}
					if x, ok := n.X.(*ast.Ident); ok && slices.Contains(starters[x.Name], n.Sel.Name) {
						fail(fset, n, "uses %s.%s: a process started outside os/exec, which the guard cannot read (rule 4)", x.Name, n.Sel.Name)
					}
					if x, ok := n.X.(*ast.Ident); ok && execName != "" && x.Name == execName && (n.Sel.Name == "Command" || n.Sel.Name == "CommandContext") && !calls[n] {
						fail(fset, n, "takes exec.%s as a value: a program the guard cannot read (rule 4)", n.Sel.Name)
					}
					if x, ok := n.X.(*ast.Ident); ok && execName != "" && x.Name == execName && n.Sel.Name == "Cmd" {
						fail(fset, n, "builds an exec.Cmd by hand: a program the guard cannot read (rule 4)")
					}
				case *ast.Ident:
					if r := guardRunnerNamed(n.Name); r && fn != n.Name && !guardRunnerCalled(f, n) {
						fail(fset, n, "takes the runner %s as a value: its call sites are what the guard checks (rule 4)", n.Name)
					}
				case *ast.CallExpr:
					guardCall(fset, name, fn, execName, n, fail, seen.programs)
				}
				return true
			})
		}
	}
	return found, seen
}

func guardScreen(p string) bool {
	for _, s := range []string{"github.com/charmbracelet/bubbletea", "github.com/charmbracelet/lipgloss", "github.com/charmbracelet/bubbles", "github.com/gdamore/tcell"} {
		if p == s || strings.HasPrefix(p, s+"/") {
			return true
		}
	}
	return false
}

func guardRunnerNamed(n string) bool {
	return slices.ContainsFunc(guardRunners, func(r struct{ file, fn, param, why string }) bool { return r.fn == n })
}

// guardRunnerCalled: id is the called function of a call, or the value of the
// runner's own field (newGitHubCheck's run: runProbe), which the call-site
// rule covers.
func guardRunnerCalled(f *ast.File, id *ast.Ident) bool {
	ok := false
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if n.Fun == id {
				ok = true
			}
		case *ast.KeyValueExpr:
			if k, isID := n.Key.(*ast.Ident); isID && n.Value == id && k.Name == "run" && id.Name == "runProbe" {
				ok = true
			}
		}
		return !ok
	})
	return ok
}

func guardCall(fset *token.FileSet, file, fn, execName string, c *ast.CallExpr, fail func(*token.FileSet, ast.Node, string, ...any), programs map[string]bool) {
	// A call of exec.Command or exec.CommandContext.
	if s, ok := c.Fun.(*ast.SelectorExpr); ok && execName != "" {
		if x, ok := s.X.(*ast.Ident); ok && x.Name == execName && (s.Sel.Name == "Command" || s.Sel.Name == "CommandContext") {
			i := 0
			if s.Sel.Name == "CommandContext" {
				i = 1
			}
			if len(c.Args) <= i {
				fail(fset, c, "exec.%s with no program", s.Sel.Name)
				return
			}
			if id, ok := c.Args[i].(*ast.Ident); ok {
				for _, r := range guardRunners {
					if r.file == file && r.fn == fn && r.param == id.Name {
						return // a runner: its call sites are checked
					}
				}
			}
			guardProgram(fset, c.Args[i], "exec."+s.Sel.Name, fail, programs)
			return
		}
	}
	// A call of a runner.
	for _, r := range guardRunnerCalls {
		var match bool
		switch fun := c.Fun.(type) {
		case *ast.Ident:
			match = r.fn != "" && fun.Name == r.fn
		case *ast.SelectorExpr:
			_, onCall := fun.X.(*ast.CallExpr) // exec.Command(...).Output() is not the runner's field
			match = r.sel != "" && fun.Sel.Name == r.sel && r.file == file && !onCall
		}
		if !match || r.arg < 0 {
			continue
		}
		if len(c.Args) <= r.arg {
			fail(fset, c, "a runner's call with no program")
			return
		}
		guardProgram(fset, c.Args[r.arg], "a runner's call", fail, programs)
		return
	}
}

func guardProgram(fset *token.FileSet, e ast.Expr, what string, fail func(*token.FileSet, ast.Node, string, ...any), programs map[string]bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		fail(fset, e, "%s: a program the guard cannot read (rule 4): name it as a literal of guardPrograms, or as a runner", what)
		return
	}
	p, _ := strconv.Unquote(lit.Value)
	if slices.Contains(guardShells, path.Base(p)) {
		fail(fset, e, "%s runs %s, a shell (rule 5): the runtime never runs one", what, p)
		return
	}
	if _, ok := guardPrograms[p]; !ok {
		fail(fset, e, "%s runs %s, not in guardPrograms (rule 4)", what, p)
		return
	}
	programs[p] = true
}
