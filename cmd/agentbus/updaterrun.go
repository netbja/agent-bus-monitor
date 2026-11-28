package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/buildinfo"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/setup"
	"github.com/netbja/agent-bus-monitor/update"
)

// The updater service's process (quiet-update-auto §4.1, step 4d): `agentbus
// service run updater`, one per host, no project. It is not the dispatcher:
// an update builds code, and the dispatcher never runs repository code. Its
// loop holds no broker connection; each pass is a child process, the agentbus
// installed now, which connects as the CLI (§6).

// updaterDeps is what runUpdater needs, injected for tests.
type updaterDeps struct {
	Repo   string // the managed build's checkout; "" is a developer build
	Bin    string // the agentbus installed now, <bindir>/agentbus
	Getenv func(string) string
}

// mainUpdaterRun is the unit's entry: the loop until SIGTERM or SIGINT, its
// log on stderr (the unit's journal, launchd's log).
func mainUpdaterRun(args []string) int {
	if err := loadUpdaterEnv(args, os.Setenv); err != nil {
		fmt.Fprintln(os.Stderr, "updater: "+err.Error()+"; not restarting")
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	bin := buildinfo.Bin
	if bin != "" {
		bin = filepath.Join(filepath.Dir(bin), "agentbus")
	}
	return runUpdater(ctx, updaterDeps{Repo: buildinfo.Repo, Bin: bin, Getenv: os.Getenv}, os.Stderr)
}

// loadUpdaterEnv reads the unit's arguments, [--env <file>]: the file's
// values go into this process's environment (setenv), so each pass, a child,
// has the PATH and the broker settings agentbus up recorded (a unit's own
// environment is minimal). No file named is no change; anything else, or a
// file that cannot be read, is an invalid configuration.
func loadUpdaterEnv(args []string, setenv func(k, v string) error) error {
	switch {
	case len(args) == 0:
		return nil
	case len(args) != 2 || args[0] != "--env":
		return fmt.Errorf("usage: agentbus service run updater [--env <file>]")
	}
	env, err := service.LoadEnvFile(args[1])
	if err != nil {
		return fmt.Errorf("invalid configuration: %v (agentbus up rewrites it)", err)
	}
	for k, v := range env {
		if err := setenv(k, v); err != nil {
			return err
		}
	}
	return nil
}

// runUpdater runs the loop. Exit 0 is final for the service manager: a
// developer build has no updater, and a stop ends the loop.
func runUpdater(ctx context.Context, d updaterDeps, log io.Writer) int {
	if d.Repo == "" || d.Bin == "" {
		fmt.Fprintln(log, "updater: a developer build (no managed checkout): nothing to update; not restarting")
		return 0
	}
	updates := update.Dir(setup.StateDir(d.Getenv))
	l := update.Loop{
		Now:    time.Now,
		Auto:   func() string { return updatesAuto(d.Getenv) },
		Window: func() string { return updatesWindow(d.Getenv) },
		State:  func() update.AutoState { return update.ReadAuto(updates) },
		Pass:   func(ctx context.Context, check bool) int { return updaterPass(ctx, d.Bin, check, log) },
		Log:    log,
	}
	fmt.Fprintf(log, "updater: running %s update --auto in the window, every %s\n", d.Bin, update.LoopEvery)
	l.Run(ctx)
	fmt.Fprintln(log, "updater: stopped")
	return 0
}

// updaterPass runs bin update --auto (--check when check) in its own process
// group, its output in log, and returns its exit code. When ctx ends first it
// returns -1 and leaves the pass running: stopping the loop never kills a
// pass in flight (§4.1).
func updaterPass(ctx context.Context, bin string, check bool, log io.Writer) int {
	cmd := updaterCmd(bin, check, log)
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(log, "updater: the pass cannot start: "+err.Error())
		return 1
	}
	done := make(chan int, 1)
	go func() {
		err := cmd.Wait()
		if err == nil {
			done <- 0
			return
		}
		if ee, ok := err.(*exec.ExitError); ok {
			done <- ee.ExitCode()
			return
		}
		fmt.Fprintln(log, "updater: the pass: "+err.Error())
		done <- 1
	}()
	select {
	case code := <-done:
		return code
	case <-ctx.Done():
		fmt.Fprintf(log, "updater: stopping; the pass (pid %d) runs on\n", cmd.Process.Pid)
		return -1
	}
}

// updaterCmd is a pass's command: bin update --auto (--check when check),
// its output in log, in its own process group, so a signal to the loop's
// group (a manager's stop, a terminal's Ctrl-C) never reaches the pass.
func updaterCmd(bin string, check bool, log io.Writer) *exec.Cmd {
	args := []string{"update", "--auto"}
	if check {
		args = append(args, "--check")
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// updatesWindow is updates.window from config.toml (no env variable).
func updatesWindow(getenv func(string) string) string {
	f := config.Load(config.Path(getenv), "config.toml")
	r := config.Resolver{Getenv: getenv, Files: []*config.File{f}}
	return r.String("updates.window")
}
