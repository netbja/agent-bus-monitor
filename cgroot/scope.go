package cgroot

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Scope mode (resource-limits §12.5, step 4b): the holder starts a pane's
// command through systemd-run --user --scope, which makes the scope with its
// limits for its own pid, then execs agentbus agent-enter, which moves that
// pid into the scope's client leaf and execs the client. Every step keeps
// the pid, so the holder's pty, session, process group and Wait are the
// client's, as without a scope. User-mode knowledge (--user, the unit name)
// stays in this package.

// ScopeSlice is the slice that holds every agent's scope (cap A, §12.3).
const ScopeSlice = "agentbus.slice"

// ScopeKey is a build key as it sits in a scope's name: every character
// outside [A-Za-z0-9._] is "_", so the name's dashes still split it (a
// "+dirty" key, a key with a dash) and systemd accepts it.
func ScopeKey(key string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' {
			return r
		}
		return '_'
	}, key)
}

// ScopeUnit is the scope of one terminal: agentbus-<p>-<key>-<role>-<term>.scope;
// the build key is in it so a holder's start sweep can tell its own build's
// scopes from another's (§12.5, r5).
func ScopeUnit(project, key, role, term string) string {
	return "agentbus-" + project + "-" + ScopeKey(key) + "-" + role + "-" + term + ".scope"
}

// ScopeOpts are a scope launch's parts.
type ScopeOpts struct {
	SystemdRun         string   // the systemd-run binary
	Unit               string   // ScopeUnit
	MemoryMax, SwapMax string   // cap B (limits.agent_memory_max, agent_swap_max); "" leaves it unset
	Enter              []string // the agent-enter command: [<agentbus>, "agent-enter"]
	Argv               []string // the client's own argv
}

// ScopeArgv is the scope launch (§12.5): the limits are the scope's
// properties, set before its command runs.
func ScopeArgv(o ScopeOpts) []string {
	a := []string{o.SystemdRun, "--user", "--scope", "--quiet", "--slice=" + ScopeSlice, "--unit=" + o.Unit, "-p", "Delegate=yes"}
	if o.MemoryMax != "" {
		a = append(a, "-p", "MemoryMax="+o.MemoryMax)
	}
	if o.SwapMax != "" {
		a = append(a, "-p", "MemorySwapMax="+o.SwapMax)
	}
	a = append(a, "-p", "OOMPolicy=continue", "--")
	a = append(a, o.Enter...)
	return append(append(a, "--"), o.Argv...)
}

// Enter moves pid, in an agentbus scope's root, into the scope's client
// leaf, then enables +memory +pids +cpu in the scope (legal once no process
// is left in its root: cgroup v2's no-internal-process rule).
func (fs FS) Enter(pid int) error {
	rel, why := fs.own()
	if why != "" {
		return fmt.Errorf("cgroot: %s", why)
	}
	base := filepath.Base(rel)
	if !strings.HasPrefix(base, "agentbus-") || !strings.HasSuffix(base, ".scope") {
		return fmt.Errorf("cgroot: not in an agentbus scope (%s)", rel)
	}
	dir := filepath.Join(fs.Mount, rel)
	client := filepath.Join(dir, "client")
	if err := os.Mkdir(client, 0o755); err != nil && !os.IsExist(err) {
		return fmt.Errorf("cgroot: %w", err)
	}
	if err := os.WriteFile(filepath.Join(client, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return fmt.Errorf("cgroot: into client: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cgroup.subtree_control"), []byte("+memory +pids +cpu"), 0o644); err != nil {
		return fmt.Errorf("cgroot: controllers: %w", err)
	}
	return nil
}

// EnterAndExec is agentbus agent-enter: Enter this process, then exec argv
// with env, keeping the pid. A failed Enter is said on warn and the client
// runs uncapped (a launch is never refused, §4's fallback); it returns only
// when the exec itself fails.
func EnterAndExec(fs FS, argv, env []string, warn io.Writer) error {
	if len(argv) == 0 {
		return fmt.Errorf("cgroot: agent-enter: no command")
	}
	if err := fs.Enter(os.Getpid()); err != nil {
		fmt.Fprintf(warn, "agent-enter: %v: the client runs without its cap\n", err)
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(path, argv, entered(env))
}

// EnterFDEnv names the fd on which agent-enter tells the holder that the
// scope launch reached it: one byte, then the fd is closed. A pipe that
// closes with no byte means systemd-run ended before its exec, and the
// holder starts the pane without a scope (§12.5: a launch is never refused).
const EnterFDEnv = "AGENTBUS_ENTER_FD"

// entered says so on EnterFDEnv's fd, closes it, and is env without the
// variable: the client neither sees it nor holds the fd.
func entered(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		v, ok := strings.CutPrefix(kv, EnterFDEnv+"=")
		if !ok {
			out = append(out, kv)
			continue
		}
		if fd, err := strconv.Atoi(v); err == nil && fd > 2 {
			_, _ = syscall.Write(fd, []byte("y"))
			_ = syscall.Close(fd)
		}
	}
	return out
}
