package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/netbja/agent-bus-monitor/setup"
)

// `agentbus setup [--repo <dir>] install|update|uninstall|status [setup.sh's
// flags]` (spec docs/superpowers/specs/2026-10-28-go-installer-design.md):
// the Go port of setup.sh, run by the thin scripts/setup-go.sh. Step 1 of
// the rollout: the flags, help, refusals and preflight are ported; the steps
// after the preflight follow in busmon-installer-2..7.

// execRunner is the real setup.Runner, the only one: every external command
// setup runs goes through it.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, c setup.Cmd) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Dir = c.Dir
	return cmd.CombinedOutput()
}

func (execRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func runSetup(args []string, getenv func(string) string, r setup.Runner, stdout, stderr io.Writer, color bool) int {
	repo := ""
	if len(args) >= 2 && args[0] == "--repo" {
		repo, args = args[1], args[2:]
	}
	if repo == "" {
		repo, _ = os.Getwd()
	}
	o := setup.NewOutput(stdout, stderr, color)
	opt, code := setup.ParseArgs(args, getenv, o)
	if code >= 0 {
		return code
	}
	if !isFile(filepath.Join(repo, "go.mod")) || !isDir(filepath.Join(repo, "cmd", "agentbus")) {
		o.Fatal("setup.sh must run from a clone of agent-bus-monitor (no go.mod next to it in " + repo + ")")
		return 1
	}
	if opt.Sub == "install" {
		missing := setup.Preflight(context.Background(), r, o, opt, goModVersion(repo), runtime.GOOS, runtime.GOARCH)
		if missing > 0 && !opt.DryRun {
			o.Fatal(itoa(missing) + " required tool(s) missing; install them and re-run.")
			return 1
		}
	}
	o.Fatal("agentbus setup " + opt.Sub + ": not ported yet past the preflight (busmon-installer-2..7); use ./setup.sh " + opt.Sub)
	return 1
}

// goModVersion is go.mod's go line, as setup.sh reads it.
func goModVersion(repo string) string {
	b, err := os.ReadFile(filepath.Join(repo, "go.mod"))
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "go "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func isFile(p string) bool { st, err := os.Stat(p); return err == nil && st.Mode().IsRegular() }
func isDir(p string) bool  { st, err := os.Stat(p); return err == nil && st.IsDir() }
