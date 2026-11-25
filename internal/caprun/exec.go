package caprun

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"strconv"
	"syscall"
)

// Exec is the helper's side: args are <rlimit bytes> -- <command...>. It
// raises its own oom_score_adj to +500 (no privilege needed to raise it; the
// command inherits it), sets RLIMIT_AS when above 0, then execs the command.
// It returns only on a failure: 127 for a command not found, 2 for bad args.
//
// Everything that allocates (the path, argv and environment as C strings)
// is done before the limit: once it is set, this Go process may already map
// more than the cap, and any allocation could abort the runtime (foureyes).
// execLimited sets it and execs with no Go allocation between the two.
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
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		fmt.Fprintf(errw, "agentbus run: %s: %v\n", argv[0], err)
		return 126
	}
	av, err := syscall.SlicePtrFromStrings(argv)
	if err != nil {
		fmt.Fprintf(errw, "agentbus run: %s: %v\n", argv[0], err)
		return 126
	}
	env := os.Environ()
	ev, err := syscall.SlicePtrFromStrings(env)
	if err != nil {
		fmt.Fprintf(errw, "agentbus run: %s: %v\n", argv[0], err)
		return 126
	}
	// No collection may start between the limit and the exec: the
	// collector maps memory of its own.
	debug.SetGCPercent(-1)
	lim := syscall.Rlimit{Cur: uint64(n), Max: uint64(n)}
	stage, err := execLimited(cExec{path, argv, env, p, av, ev}, n > 0, &lim)
	fmt.Fprintf(errw, "agentbus run: %s: %s: %v\n", argv[0], stage, err)
	return 126
}

// cExec is one exec's path, argv and environment, as Go strings and as the
// NUL-terminated C strings execve takes, built before any limit is set.
type cExec struct {
	path       string
	argv, envv []string
	cpath      *byte
	cargv      []*byte
	cenvv      []*byte
}
