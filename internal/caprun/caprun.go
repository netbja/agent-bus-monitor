// Package caprun runs one command under cap C (spec
// docs/superpowers/specs/2026-11-25-agent-resource-limits-design.md §6): its
// own process group, a time cap that kills the whole group, oom_score_adj +500
// so the command is the kernel's preferred victim at a wider cap, and a memory
// cap whose mechanism depends on where it runs.
package caprun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// HelperArg is the hidden subcommand agentbus re-executes itself with to set
// the command's limits before it execs it: Go runs no code between fork and
// exec, and agentbus run itself stays unlimited, so it can always report.
const HelperArg = "__capexec"

// Where is where agentbus run runs, read from its own cgroup.
type Where int

const (
	Outside     Where = iota // no cgroup, or one outside a user manager
	UserManager              // the person's user manager, no holder: systemd-run
	InHolder                 // a holder unit, its terminal cgroup included
)

func (w Where) String() string {
	return [...]string{"outside", "user manager", "holder"}[w]
}

// Classify reads /proc/self/cgroup's text: the unified (0::) line under a
// holder unit (agentbus-holder.*.service, any depth below it), under a user
// manager (user@<uid>.service), or neither. A holder is never escaped with
// systemd-run: caps A and B must keep covering the command.
func Classify(cgroup string) Where {
	for line := range strings.SplitSeq(cgroup, "\n") {
		path, ok := strings.CutPrefix(line, "0::")
		if !ok {
			continue
		}
		where := Outside
		for el := range strings.SplitSeq(path, "/") {
			switch {
			case strings.HasPrefix(el, "agentbus-holder.") && strings.HasSuffix(el, ".service"):
				return InHolder
			case strings.HasPrefix(el, "user@") && strings.HasSuffix(el, ".service"):
				where = UserManager
			}
		}
		return where
	}
	return Outside
}

// Limits are one run's caps; zero is none.
type Limits struct {
	Mem     int64  // bytes
	MemText string // as given ("8G"), for systemd-run and the lines
	Time    time.Duration
	CPU     int // percent; only systemd-run applies it
}

// Deps are the process's surroundings.
type Deps struct {
	Self           string        // the executable that answers HelperArg
	Cgroup         func() string // /proc/self/cgroup's text, "" without one
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// Run runs argv under l and returns its exit status: the command's own, or
// 137 with one line on stderr when it was killed.
func Run(ctx context.Context, l Limits, argv []string, d Deps) int {
	if len(argv) == 0 {
		fmt.Fprintln(d.Stderr, "agentbus run: no command")
		return 2
	}
	where := Classify(d.Cgroup())
	rlimit := l.Mem
	cmdArgv := argv
	if where == UserManager {
		if sr, err := exec.LookPath("systemd-run"); err == nil {
			cmdArgv, rlimit = append(scopeArgv(sr, l), argv...), 0
		} else {
			where = Outside
		}
	}
	helper := append([]string{HelperArg, strconv.FormatInt(rlimit, 10), "--"}, cmdArgv...)
	cmd := exec.Command(d.Self, helper...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = d.Stdin, d.Stdout, d.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(d.Stderr, "agentbus run:", err)
		return 127
	}
	pgid := cmd.Process.Pid
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// The group is not the terminal's foreground group: an interrupt or a
	// stop of agentbus run is passed on to it.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	var deadline <-chan time.Time
	if l.Time > 0 {
		t := time.NewTimer(l.Time)
		defer t.Stop()
		deadline = t.C
	}
	timedOut := false
	var werr error
wait:
	for {
		select {
		case werr = <-done:
			break wait
		case <-deadline:
			timedOut = true
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		case s := <-sigs:
			_ = syscall.Kill(-pgid, s.(syscall.Signal))
		case <-ctx.Done():
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
	}
	return exitOf(werr, timedOut, where, l, d.Stderr)
}

// scopeArgv is systemd-run's transient scope for a shell no holder runs (§6,
// third row): a property for each limit given, swap always off with a memory
// cap. The scope execs the command in place, so the process group and
// oom_score_adj still hold.
func scopeArgv(systemdRun string, l Limits) []string {
	a := []string{systemdRun, "--user", "--scope", "--quiet", "--collect"}
	if l.Mem > 0 {
		a = append(a, "-p", "MemoryMax="+l.MemText, "-p", "MemorySwapMax=0")
	}
	if l.Time > 0 {
		a = append(a, "-p", "RuntimeMaxSec="+strconv.FormatInt(int64((l.Time+time.Second-1)/time.Second), 10))
	}
	if l.CPU > 0 {
		a = append(a, "-p", "CPUQuota="+strconv.Itoa(l.CPU)+"%")
	}
	return append(a, "--")
}

// exitOf is the status and the line, by what is known (§6, Exit): the time
// cap fired, a SIGKILL nobody here sent (the memory cap is not proven: the
// kernel names no victim), or the command's own status, with a note when it
// failed under RLIMIT_AS, where an allocation fails instead of a kill.
func exitOf(werr error, timedOut bool, where Where, l Limits, errw io.Writer) int {
	var ee *exec.ExitError
	if werr != nil && !errors.As(werr, &ee) {
		fmt.Fprintln(errw, "agentbus run:", werr)
		return 127
	}
	if timedOut {
		fmt.Fprintf(errw, "agentbus run: killed at time cap %s\n", l.Time)
		return 137
	}
	if ee == nil {
		return 0
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		sig := ws.Signal()
		if sig == syscall.SIGKILL {
			fmt.Fprintln(errw, "agentbus run: killed by signal 9 (memory cap not proven)")
		} else {
			fmt.Fprintf(errw, "agentbus run: killed by signal %d\n", int(sig))
		}
		return 128 + int(sig)
	}
	code := ee.ExitCode()
	if l.Mem > 0 && where != UserManager && code != 127 {
		fmt.Fprintf(errw, "agentbus run: exit %d under memory limit %s (RLIMIT_AS: an allocation fails; memory cap not proven)\n", code, l.MemText)
	}
	return code
}

// Exec is the helper's side: args are <rlimit bytes> -- <command...>. It sets
// RLIMIT_AS when above 0, raises its own oom_score_adj to +500 (no privilege
// needed to raise it; the command inherits it), then execs the command. It
// returns only on a failure: 127 for a command not found, 2 for bad args.
func Exec(args []string, errw io.Writer) int {
	if len(args) < 3 || args[1] != "--" {
		fmt.Fprintln(errw, "agentbus run: bad helper arguments")
		return 2
	}
	n, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || n < 0 {
		fmt.Fprintln(errw, "agentbus run: bad helper memory limit")
		return 2
	}
	argv := args[2:]
	path, err := exec.LookPath(argv[0])
	if err != nil {
		fmt.Fprintf(errw, "agentbus run: %s: command not found\n", argv[0])
		return 127
	}
	_ = os.WriteFile("/proc/self/oom_score_adj", []byte("500"), 0) // Linux only
	if n > 0 {
		if err := syscall.Setrlimit(syscall.RLIMIT_AS, &syscall.Rlimit{Cur: uint64(n), Max: uint64(n)}); err != nil {
			fmt.Fprintln(errw, "agentbus run: memory limit:", err)
			return 126
		}
	}
	err = syscall.Exec(path, argv, os.Environ())
	fmt.Fprintf(errw, "agentbus run: %s: %v\n", argv[0], err)
	return 126
}
