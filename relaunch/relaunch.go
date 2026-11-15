// Package relaunch holds what up, reload, swap and self-repair share about a
// role's agent: whether its process lives (SnapLife), the role a template line
// launches (LaunchRole), the session a relaunch may resume (ProvedSession),
// the project template, and up's per-project lock. Terminals themselves are
// the holder's (termhost); nothing here starts, types into or closes one.
package relaunch

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// Life is what is known of an agent's process.
type Life int

const (
	LifeUnknown Life = iota
	LifeAlive
	LifeDead
)

func (l Life) String() string {
	switch l {
	case LifeAlive:
		return "alive"
	case LifeDead:
		return "dead"
	}
	return "unknown"
}

// SnapLife judges an agent by its verified identity, the rule the dispatcher
// uses: the recorded pid runs with the recorded start time. A pid gone or
// reused is dead; no snapshot, no pid, or an unreadable process is unknown.
// Gone is PidListedNoMore: dispatch.ProcStart's ErrNoProcess does not wrap
// fs.ErrNotExist, and reading it as unknown made a tab whose old client
// the host still showed read alive, never waited on (reload of 2026-11-09).
func SnapLife(s bus.AgentSnapshot, has bool, procStart func(int) (uint64, error)) Life {
	if !has || s.PID <= 0 || s.PIDStart == 0 || procStart == nil {
		return LifeUnknown
	}
	start, err := procStart(s.PID)
	switch {
	case err != nil && PidListedNoMore(err):
		return LifeDead
	case err != nil:
		return LifeUnknown
	case start != s.PIDStart:
		return LifeDead
	}
	return LifeAlive
}

// Verdict is what self-repair judges of one role's terminal.
type Verdict string

const (
	VerdictAlive    Verdict = "alive"
	VerdictRunning  Verdict = "running"  // a terminal that is not an agent: something runs in it
	VerdictRelaunch Verdict = "relaunch" // in a new terminal
	VerdictCreate   Verdict = "create"   // no terminal for that role: start one
	VerdictBusy     Verdict = "skipped (busy)"
	VerdictUnknown  Verdict = "unknown"
	VerdictMissing  Verdict = "missing" // no terminal, and starting one was not asked for
)

// LaunchRole reads `[env …] …/agent-launch <role> <project> …` and returns the
// role when the line launches one for this project.
func LaunchRole(command, project string) (string, bool) {
	f := strings.Fields(command)
	for i, w := range f {
		if filepath.Base(w) != "agent-launch" || i+2 >= len(f) {
			continue
		}
		if f[i+2] == project && bus.ValidName(f[i+1]) {
			return f[i+1], true
		}
	}
	return "", false
}

// ProvedSession is the session a relaunch of the agent may resume: the one
// proved with its process (IdentSession), for a client whose session source
// is verified (self-repair step 5), else "".
func ProvedSession(s bus.AgentSnapshot) string {
	if !rolecfg.ResumeVerified(s.Client) || !bus.ValidSessionID(s.IdentSession) {
		return ""
	}
	return s.IdentSession
}

// IsBusmonTab: the template tab named busmon that runs this project's busmon
// (its first word's base name busmon, --project <project>). No other tab.
func IsBusmonTab(t Tab, project string) bool {
	if t.Name != "busmon" {
		return false
	}
	w, err := rolecfg.ShellSplit(t.Command)
	if err != nil || len(w) == 0 || filepath.Base(w[0]) != "busmon" {
		return false
	}
	for i, a := range w[1:] {
		if a == "--project="+project || a == "--project" && i+2 < len(w) && w[i+2] == project {
			return true
		}
	}
	return false
}

// Tab is one tab of a project template.
type Tab struct {
	Name    string `toml:"name"`
	Command string `toml:"command"`
}

// Template is a project template (~/.config/agentbus/projects/<p>.toml).
type Template struct {
	Name       string `toml:"name"`
	WorkingDir string `toml:"working_dir"`
	Tabs       []Tab  `toml:"tabs"`
}

// PidListedNoMore: a pid's start could not be read because /proc, readable
// here, lists no such pid: dispatch.ProcStart's ErrNoProcess, which it returns
// only after that same check (backup-live-check: it does not wrap
// fs.ErrNotExist), or a bare fs.ErrNotExist. Any other error (no /proc, a
// failed read) proves nothing: the project is refused.
func PidListedNoMore(err error) bool {
	if errors.Is(err, dispatch.ErrNoProcess) {
		return true
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	_, serr := os.Stat("/proc/self/stat")
	return serr == nil
}
