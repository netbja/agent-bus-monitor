package service

import (
	"errors"
	"github.com/netbja/agent-bus-monitor/config"
	"os"
	"os/exec"
	"path/filepath"
)

// Spec is one service of one project, as a unit runs it:
//
//	<Exe> --project <Project> service run <Kind> --env <EnvFile> [--stderr] <Args…>
//
// in WorkDir. Exe is the absolute path of the agentbus that wrote the unit;
// EnvFile is named explicitly because the manager's environment may resolve
// the XDG directories differently from the shell that ran `agentbus up`.
//
// AtLogin is the services.start_at_login option (self-repair step 9): the
// unit is written started at login (login.go).
type Spec struct {
	Project, Kind, Exe, WorkDir, EnvFile string
	Args                                 []string
	Paths                                Paths
	AtLogin                              bool
}

// argv is the service command; stderr is true where stderr is a journal.
func (s Spec) argv(stderr bool) []string {
	a := []string{s.Exe, "--project", s.Project, "service", "run", s.Kind}
	if s.EnvFile != "" {
		a = append(a, "--env", s.EnvFile)
	}
	if stderr {
		a = append(a, "--stderr")
	}
	return append(a, s.Args...)
}

// Argv is the service command, as a manager without a journal runs it
// (holder.Supervised: never --stderr, its output already is a file).
func (s Spec) Argv() []string { return s.argv(false) }

// State is what the manager says of one unit.
type State struct {
	Installed bool   // a unit (plist) file exists
	Active    bool   // the process runs, or is starting
	Status    string // the manager's words: "active (running)", "inactive (dead)", "failed", …
	PID       int
}

// Unit names one installed service.
type Unit struct{ Project, Kind string }

// Manager is the operating system's service manager, through its own CLI.
type Manager interface {
	Name() string
	// Install writes or updates the unit; it reports whether anything changed.
	Install(s Spec) (bool, error)
	Start(project, kind string) error
	Stop(project, kind string) error
	// Remove stops the service and deletes its unit. A missing unit is not an error.
	Remove(project, kind string) error
	State(project, kind string) (State, error)
	// List returns every agentbus service installed for this user.
	List() ([]Unit, error)
}

// Runner runs the manager's CLI (systemctl or launchctl) with these arguments.
type Runner func(args ...string) ([]byte, error)

// CommandRunner runs bin for real, merging stderr into the output.
func CommandRunner(bin string) Runner {
	return func(args ...string) ([]byte, error) {
		return exec.Command(bin, args...).CombinedOutput()
	}
}

// ManagerChoice is the machine's services.manager: the environment, then
// config.toml, then auto (herdr exit, spec 4.4b).
func ManagerChoice(getenv func(string) string) string {
	r := config.Resolver{Getenv: getenv, Files: []*config.File{config.Load(config.Path(getenv), "config.toml")}}
	return r.String("services.manager")
}

// managerReason names where a forced choice came from, for why.
func managerReason(getenv func(string) string, choice string) string {
	if env := config.EnvName("services.manager"); getenv(env) == choice {
		return env + "=" + choice
	}
	return "services.manager = " + choice + " (config.toml)"
}

// Unmanaged is the manager where none runs (herdr exit 6c3, spec 4.4b):
// holder.Supervised, which package holder registers (it imports service, so
// service cannot name it). Nil — a binary that does not link holder — keeps
// the answer of before: no manager, and why.
var Unmanaged func(paths Paths, getenv func(string) string) Manager

// systemdBooted: this Linux runs systemd as init (sd_booted's test).
var systemdBooted = func() bool {
	st, err := os.Lstat("/run/systemd/system")
	return err == nil && st.IsDir()
}

// Detect finds the manager to use: services.manager (systemd, launchd or
// none) forces one; auto is systemd --user on Linux when its user instance
// answers, launchd on macOS, and otherwise the manager without one
// (Unmanaged). A systemd that runs as init but whose user instance does not
// answer is not "no systemd": that stays an error, so no supervisor ever
// starts beside the units. With no manager at all, why says what is missing,
// for the explicit fallback to herdr tabs.
func Detect(goos string, run Runner, getenv func(string) string) (m Manager, why string) {
	paths, err := PathsFrom(getenv)
	if err != nil {
		return nil, err.Error()
	}
	forced := true
	switch choice := ManagerChoice(getenv); choice {
	case "none":
		if Unmanaged != nil {
			return Unmanaged(paths, getenv), ""
		}
		return nil, managerReason(getenv, choice)
	case "systemd":
		goos = "linux"
	case "launchd":
		goos = "darwin"
	default:
		forced = false
	}
	switch goos {
	case "linux":
		if out, err := run("--user", "show-environment"); err != nil {
			if !forced && Unmanaged != nil && !systemdBooted() {
				return Unmanaged(paths, getenv), ""
			}
			return nil, "systemd --user is not available (" + firstLine(out, err) + ")"
		}
		return Systemd{Run: run, Paths: paths}, ""
	case "darwin":
		uid := os.Getuid()
		if out, err := run("print", "gui/"+itoa(uid)); err != nil {
			return nil, "launchd has no GUI session for this user (" + firstLine(out, err) + ")"
		}
		return Launchd{Run: run, Paths: paths, UID: uid}, ""
	}
	if Unmanaged != nil {
		return Unmanaged(paths, getenv), ""
	}
	return nil, "no supported service manager on " + goos
}

// Local is the manager a check may trust (reviewer-codex): nil without an
// error only when the person chose none (services.manager); a manager that
// cannot be reached, or a system with none, is an error, never "no unit
// here".
func Local(goos string, run Runner, getenv func(string) string) (Manager, error) {
	m, why := Detect(goos, run, getenv)
	switch {
	case m != nil:
		return m, nil
	case ManagerChoice(getenv) == "none":
		return nil, nil
	}
	return nil, errors.New(why + "; services.manager = none (" + config.EnvName("services.manager") + "=none) says this machine runs no services")
}

// ManagerBin is the CLI Detect's runner should call on goos.
func ManagerBin(goos string, getenv func(string) string) string {
	switch ManagerChoice(getenv) {
	case "systemd":
		goos = "linux"
	case "launchd":
		goos = "darwin"
	}
	if goos == "darwin" {
		return "launchctl"
	}
	return "systemctl"
}

// writeIfChanged writes content to path (0644, it holds no secret) when it
// differs, atomically.
func writeIfChanged(path, content string) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && string(old) == content {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, path)
}
