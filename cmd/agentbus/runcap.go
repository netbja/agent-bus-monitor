package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/internal/caprun"
)

const capUsage = "usage: agentbus run --cap [--mem <size>] [--time <dur>] [--cpu <pct>] -- <command> [args...]"

// isCapRun: agentbus run --cap, as the first two words of the raw argv. main
// asks it before any parser reads a word: the command's own flags (-p,
// --help) are not agentbus's.
func isCapRun(args []string) bool {
	return len(args) >= 2 && args[0] == "run" && args[1] == "--cap"
}

// runCap is agentbus run --cap (agent-resource-limits §6): no project, no
// broker.
func runCap(args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	l, argv, err := capLimits(args, getenv)
	if err != nil {
		fmt.Fprintln(stderr, "agentbus run:", err)
		return 2
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "agentbus run: cannot find this agentbus:", err)
		return 2
	}
	cgroup := func() string {
		b, _ := os.ReadFile("/proc/self/cgroup")
		return string(b)
	}
	return caprun.Run(context.Background(), l, argv, caprun.Deps{Self: self, Cgroup: cgroup, Stdin: stdin, Stdout: stdout, Stderr: stderr})
}

// capLimits reads the flags before "--" and the person's limits.command_*
// keys: a flag may only narrow a key's cap (an agent cannot raise its own),
// and "--mem off" is taken only where the key is off.
func capLimits(args []string, getenv func(string) string) (caprun.Limits, []string, error) {
	r := config.Machine(getenv)
	keyMem, keyTime, keyCPU := r.String("limits.command_memory"), r.Duration("limits.command_time"), r.Int("limits.command_cpu")
	var l caprun.Limits
	if keyMem != "off" {
		n, err := config.ParseSize(keyMem)
		if err != nil {
			return l, nil, fmt.Errorf("limits.command_memory %q: %v", keyMem, err)
		}
		l.Mem, l.MemText = n, keyMem
	}
	l.Time, l.CPU = keyTime, keyCPU
	usage := errors.New(capUsage)
	i := 0
	for ; i < len(args) && args[i] != "--"; i += 2 {
		if i+1 >= len(args) || args[i+1] == "--" {
			return l, nil, usage
		}
		v := args[i+1]
		switch args[i] {
		case "--mem":
			if v == "off" {
				if keyMem != "off" {
					return l, nil, fmt.Errorf("--mem off: only where the person set limits.command_memory to off (it is %s)", keyMem)
				}
				l.Mem, l.MemText = 0, ""
				continue
			}
			n, err := config.ParseSize(v)
			if err != nil {
				return l, nil, fmt.Errorf("--mem %s: %v", v, err)
			}
			if keyMem != "off" && n > l.Mem {
				return l, nil, fmt.Errorf("--mem %s is above limits.command_memory (%s): a flag only narrows the person's cap", v, keyMem)
			}
			l.Mem, l.MemText = n, v
		case "--time":
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return l, nil, fmt.Errorf("--time %s: want a duration above 0, such as \"5m\"", v)
			}
			if keyTime > 0 && d > keyTime {
				return l, nil, fmt.Errorf("--time %s is above limits.command_time (%s): a flag only narrows the person's cap", v, keyTime)
			}
			l.Time = d
		case "--cpu":
			n, err := strconv.Atoi(strings.TrimSuffix(v, "%"))
			if err != nil || n <= 0 {
				return l, nil, fmt.Errorf("--cpu %s: want a percentage above 0, such as 200", v)
			}
			if keyCPU > 0 && n > keyCPU {
				return l, nil, fmt.Errorf("--cpu %s is above limits.command_cpu (%d): a flag only narrows the person's cap", v, keyCPU)
			}
			l.CPU = n
		default:
			return l, nil, usage
		}
	}
	if i >= len(args) || i+1 >= len(args) {
		return l, nil, usage
	}
	return l, args[i+1:], nil
}
