package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/service"
)

// superviseDeps: the records' manager, the supervision itself (Run, nil:
// s.Run), the kill of a dead holder's recorded groups (Kill) and the cutoff
// clock (Now), replaced by tests.
type superviseDeps struct {
	Manager holder.Supervised
	Run     func(ctx context.Context, s service.Supervisor) bool
	Kill    func(getenv func(string) string, project, key string, cutoff uint64) (int, error)
	Now     func() uint64
}

func realSuperviseDeps() superviseDeps {
	paths, _ := service.PathsFrom(os.Getenv)
	return superviseDeps{
		Manager: holder.Supervised{Paths: paths},
		Run:     func(ctx context.Context, s service.Supervisor) bool { return s.Run(ctx) },
		Kill: func(getenv func(string) string, project, key string, cutoff uint64) (int, error) {
			return holder.KillRecordedGroups(getenv, project, key, cutoff, holder.KillGroup)
		},
		Now: holder.NowTicks,
	}
}

// runSupervise is `agentbus --project P service supervise holder P <key>`,
// what holder.Supervised starts detached where no service manager runs
// (herdr exit spec 4.4b): it runs the build's holder under the units' policy
// (service.Supervisor: restart after 2 s on a non-zero exit, at most 5 starts
// in 300 s, an exit 0 is final) and, after each exit, kills the process
// groups the holder recorded for its terminals — there is no cgroup to do it.
// SIGTERM stops it: its holder gets SIGTERM and is waited for. It exits 1
// when it gave up, 2 on bad arguments, else 0. Only the holder is supervised
// yet; dispatch and missions come with the wiring (6c3).
func runSupervise(project string, args []string, d superviseDeps, stderr io.Writer) int {
	if len(args) != 3 || args[0] != service.Holder || args[1] != project {
		fmt.Fprintln(stderr, "usage: agentbus --project <p> service supervise holder <p> <build key>")
		return 2
	}
	key := args[2]
	argv, dir, envFile, err := d.Manager.HolderCommand(project, key)
	if err != nil {
		fmt.Fprintf(stderr, "supervise: %v\n", err)
		return 2
	}
	// The run directory the holder records its groups in is the one its env
	// file gives it (holderConfig): read the same way, at each start.
	getenv := os.Getenv
	command := func() *exec.Cmd {
		if env, err := service.LoadEnvFile(envFile); err == nil {
			getenv = func(k string) string {
				if v, ok := env[k]; ok {
					return v
				}
				return os.Getenv(k)
			}
		}
		c := exec.Command(argv[0], argv[1:]...)
		c.Dir, c.Stdout, c.Stderr = dir, stderr, stderr
		return c
	}
	sup := service.Supervisor{
		Command: command,
		Log:     stderr,
		AfterExit: func(code int) {
			cutoff := d.Now() // first: what starts after the exit is not the holder's
			n, err := d.Kill(getenv, project, key, cutoff)
			switch {
			case err != nil:
				fmt.Fprintf(stderr, "supervise: the holder exited %d; its recorded groups: %d killed, %v\n", code, n, err)
			case n > 0:
				fmt.Fprintf(stderr, "supervise: the holder exited %d; %d processes of its terminals killed\n", code, n)
			}
		},
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(stderr, "supervise: holder of project %s, build %s (pid %d)\n", project, key, os.Getpid())
	if d.Run(ctx, sup) {
		return 1
	}
	return 0
}
