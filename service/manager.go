package service

import (
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
type Spec struct {
	Project, Kind, Exe, WorkDir, EnvFile string
	Args                                 []string
	Paths                                Paths
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

// Detect finds the manager to use: AGENTBUS_SERVICE_MANAGER (systemd, launchd
// or none) forces one; otherwise systemd --user on Linux when its user
// instance answers, launchd on macOS. With none, why says what is missing,
// for the explicit fallback to herdr tabs.
func Detect(goos string, run Runner, getenv func(string) string) (m Manager, why string) {
	paths, err := PathsFrom(getenv)
	if err != nil {
		return nil, err.Error()
	}
	switch getenv("AGENTBUS_SERVICE_MANAGER") {
	case "none":
		return nil, "AGENTBUS_SERVICE_MANAGER=none"
	case "systemd":
		goos = "linux"
	case "launchd":
		goos = "darwin"
	}
	switch goos {
	case "linux":
		if out, err := run("--user", "show-environment"); err != nil {
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
	return nil, "no supported service manager on " + goos
}

// ManagerBin is the CLI Detect's runner should call on goos.
func ManagerBin(goos string, getenv func(string) string) string {
	switch getenv("AGENTBUS_SERVICE_MANAGER") {
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
