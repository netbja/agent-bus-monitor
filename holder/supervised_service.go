package holder

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

// Supervised is also the manager of dispatch and missions where there is
// none (herdr exit 6c3, spec 4.4b): a service.Manager whose unit is a record,
// <rundir>/agentbus/<project>/<kind>.pid, one per project like their systemd
// units (they are not per build: `up` reconfigures them in place), and whose
// running unit is a detached `agentbus service supervise <kind> <project>`.
var _ service.Manager = Supervised{}

// ManagerName is what Supervised calls itself (Manager.Name): the word up,
// service status and the {p}:services marker show.
const ManagerName = "supervisor"

// Name is the manager's name.
func (Supervised) Name() string { return ManagerName }

// Limits is what does not exist without a service manager (spec 4.4b):
// service status states it, and services.start_at_login is refused with it.
func (Supervised) Limits() string {
	return "no service manager (services.manager = none, or neither systemd nor launchd): the services run as detached supervisors; " +
		"nothing starts them at login (services.start_at_login is refused; agentbus up starts them), " +
		"and whether they survive a logout depends on the system (logind with KillUserProcesses=yes ends them)"
}

var _ service.Detached = Supervised{}

// serviceRecord is one project service's record: its unit's command and the
// supervisor that runs it.
type serviceRecord struct {
	Project, Kind, Exe, WorkDir, Env string
	Args                             []string
	PID                              int
	Start                            uint64
}

func (r serviceRecord) spec() service.Spec {
	return service.Spec{Project: r.Project, Kind: r.Kind, Exe: r.Exe, WorkDir: r.WorkDir, EnvFile: r.Env, Args: r.Args}
}

func (r serviceRecord) same(o serviceRecord) bool {
	return r.Project == o.Project && r.Kind == o.Kind && r.Exe == o.Exe && r.WorkDir == o.WorkDir &&
		r.Env == o.Env && slices.Equal(r.Args, o.Args)
}

// servicePath is the record of project's kind, its directories checked.
func (m Supervised) servicePath(project, kind string) (string, error) {
	if !service.ValidKind(kind) {
		return "", fmt.Errorf("supervisor: invalid service kind %q", kind)
	}
	dir, err := ProjectDir(m.getenv(), project)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, kind+".pid"), nil
}

// Install writes the service's record; its supervisor, if one runs, is kept
// (up stops and starts it on a change, as with a unit file).
func (m Supervised) Install(s service.Spec) (bool, error) {
	if !bus.ValidName(s.Project) || !service.ValidKind(s.Kind) {
		return false, fmt.Errorf("supervisor: invalid project %q or kind %q", s.Project, s.Kind)
	}
	if !filepath.IsAbs(s.Exe) || !filepath.IsAbs(s.WorkDir) {
		return false, fmt.Errorf("supervisor: %s of %s: the binary and the working directory must be absolute paths", s.Kind, s.Project)
	}
	path, err := m.servicePath(s.Project, s.Kind)
	if err != nil {
		return false, err
	}
	unlock, err := lockRecord(path)
	if err != nil {
		return false, err
	}
	defer unlock()
	r := serviceRecord{Project: s.Project, Kind: s.Kind, Exe: s.Exe, WorkDir: s.WorkDir, Env: s.EnvFile, Args: s.Args}
	old, err := readServiceRecord(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return false, err
	case r.same(old):
		return false, nil
	default:
		r.PID, r.Start = old.PID, old.Start
	}
	return true, writeServiceRecord(path, r)
}

// Start starts the service's supervisor unless it is alive.
func (m Supervised) Start(project, kind string) error {
	path, err := m.servicePath(project, kind)
	if err != nil {
		return err
	}
	unlock, err := lockRecord(path)
	if err != nil {
		return err
	}
	defer unlock()
	r, err := readServiceRecord(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s of project %s is not installed (agentbus up %s)", kind, project, project)
	}
	if err != nil {
		return err
	}
	if ok, err := m.serviceAlive(r); err != nil || ok {
		return err
	}
	log := m.Paths.SuperviseLog(project, kind)
	if err := os.MkdirAll(filepath.Dir(log), 0o700); err != nil {
		return err
	}
	spawn := m.Spawn
	if spawn == nil {
		spawn = spawnDetached
	}
	pid, err := spawn([]string{r.Exe, "--project", project, "service", "supervise", kind, project}, r.WorkDir, log)
	if err != nil {
		return fmt.Errorf("supervisor: %s of project %s: %w", kind, project, err)
	}
	start, err := m.procStart(pid)
	if err != nil {
		return fmt.Errorf("supervisor: %s of project %s (pid %d) did not stay up: %w; see %s", kind, project, pid, err, log)
	}
	r.PID, r.Start = pid, start
	return writeServiceRecord(path, r)
}

// Stop sends the service's supervisor SIGTERM (it stops the service, which
// exits 0) and waits for it. Nothing installed or running: nothing to do.
func (m Supervised) Stop(project, kind string) error {
	r, ok, err := m.serviceRunning(project, kind)
	if err != nil || !ok {
		return err
	}
	if err := signalPinned(r.PID, r.Start, syscall.SIGTERM, m.procStart); err != nil {
		return fmt.Errorf("supervisor: stopping %s of project %s (pid %d): %w", kind, project, r.PID, err)
	}
	wait := m.StopWait
	if wait == 0 {
		wait = 15 * time.Second
	}
	for deadline := time.Now().Add(wait); ; time.Sleep(50 * time.Millisecond) {
		ok, err := m.serviceAlive(r)
		if err != nil || !ok {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("supervisor: %s of project %s (pid %d) still runs %v after SIGTERM", kind, project, r.PID, wait)
		}
	}
}

// Remove stops the service and deletes its record; the lock file stays (see
// lock). A missing record is not an error.
func (m Supervised) Remove(project, kind string) error {
	if err := m.Stop(project, kind); err != nil {
		return err
	}
	path, err := m.servicePath(project, kind)
	if err != nil {
		return err
	}
	unlock, err := lockRecord(path)
	if err != nil {
		return err
	}
	defer unlock()
	// A start may have come between the stop and the lock: never orphan it.
	if _, ok, err := m.serviceRunning(project, kind); err != nil || ok {
		if err == nil {
			err = fmt.Errorf("supervisor: %s of project %s was started again while being removed", kind, project)
		}
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// State is the record's state: installed when it exists, active while its
// supervisor is alive.
func (m Supervised) State(project, kind string) (service.State, error) {
	path, err := m.servicePath(project, kind)
	if err != nil {
		return service.State{}, err
	}
	r, err := readServiceRecord(path)
	if errors.Is(err, os.ErrNotExist) {
		return service.State{Status: "not installed"}, nil
	}
	if err != nil {
		return service.State{}, err
	}
	ok, err := m.serviceAlive(r)
	if err != nil {
		return service.State{}, err
	}
	if !ok {
		return service.State{Installed: true, Status: "stopped (no supervisor)"}, nil
	}
	return service.State{Installed: true, Active: true, Status: "running (detached supervisor)", PID: r.PID}, nil
}

// List is every project's dispatch and missions records, matched on the
// recorded values, never on the file name alone.
func (m Supervised) List() ([]service.Unit, error) {
	root, err := RunAgentbusDir(m.getenv())
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []service.Unit
	for _, e := range ents {
		p := e.Name()
		if !e.IsDir() || !bus.ValidName(p) {
			continue
		}
		for _, k := range service.Kinds {
			path, err := m.servicePath(p, k)
			if err != nil {
				return nil, err
			}
			r, err := readServiceRecord(path)
			if err != nil || r.Project != p || r.Kind != k {
				continue
			}
			out = append(out, service.Unit{Project: p, Kind: k})
		}
	}
	return out, nil
}

// ServiceCommand is what the service's supervisor runs: the unit's command
// (service.Spec.Argv) in the recorded working directory.
func (m Supervised) ServiceCommand(project, kind string) (argv []string, dir string, err error) {
	path, err := m.servicePath(project, kind)
	if err != nil {
		return nil, "", err
	}
	r, err := readServiceRecord(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("%s of project %s is not installed", kind, project)
	}
	if err != nil {
		return nil, "", err
	}
	return r.spec().Argv(), r.WorkDir, nil
}

func (m Supervised) serviceRunning(project, kind string) (serviceRecord, bool, error) {
	path, err := m.servicePath(project, kind)
	if err != nil {
		return serviceRecord{}, false, err
	}
	r, err := readServiceRecord(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	ok, err := m.serviceAlive(r)
	return r, ok, err
}

func (m Supervised) serviceAlive(r serviceRecord) (bool, error) {
	ok, err := m.alive(supervisedRecord{Project: r.Project, Key: r.Kind, PID: r.PID, Start: r.Start})
	if err != nil {
		return false, fmt.Errorf("supervisor: %s of project %s (pid %d): %w", r.Kind, r.Project, r.PID, errors.Unwrap(err))
	}
	return ok, nil
}

// writeServiceRecord replaces path atomically with r (0600); one "arg" line
// per argument. A value holding a line break is refused: it would forge a
// field.
func writeServiceRecord(path string, r serviceRecord) error {
	type kv struct{ k, v string }
	vals := []kv{{"project", r.Project}, {"kind", r.Kind}, {"exe", r.Exe}, {"workdir", r.WorkDir}, {"env", r.Env}}
	for _, a := range r.Args {
		vals = append(vals, kv{"arg", a})
	}
	vals = append(vals, kv{"pid", strconv.Itoa(r.PID)}, kv{"start", strconv.FormatUint(r.Start, 10)})
	var b strings.Builder
	for _, f := range vals {
		if strings.ContainsAny(f.v, "\n\r") {
			return fmt.Errorf("supervisor: the record's %s holds a line break: refused", f.k)
		}
		b.WriteString(f.k + " " + f.v + "\n")
	}
	return writePrivate(path, b.String())
}

// readServiceRecord reads path as a private regular file of ours, never
// through a symlink; os.ErrNotExist when there is none.
func readServiceRecord(path string) (serviceRecord, error) {
	f, err := openPrivate(path, "the supervisor record")
	if err != nil {
		return serviceRecord{}, err
	}
	defer f.Close()
	var r serviceRecord
	vals := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), " ")
		if k == "arg" {
			r.Args = append(r.Args, v)
		} else {
			vals[k] = v
		}
	}
	if err := sc.Err(); err != nil {
		return serviceRecord{}, err
	}
	for _, k := range []string{"project", "kind", "exe", "workdir"} {
		if vals[k] == "" {
			return serviceRecord{}, fmt.Errorf("supervisor: the record %s has no %s", path, k)
		}
	}
	r.Project, r.Kind, r.Exe, r.WorkDir, r.Env = vals["project"], vals["kind"], vals["exe"], vals["workdir"], vals["env"]
	r.PID, _ = strconv.Atoi(vals["pid"])
	r.Start, _ = strconv.ParseUint(vals["start"], 10, 64)
	return r, nil
}
