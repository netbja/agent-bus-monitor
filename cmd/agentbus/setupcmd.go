package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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
	switch {
	case c.Attach:
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return nil, cmd.Run()
	case c.Stdout:
		return cmd.Output()
	}
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
		return runInstall(context.Background(), r, o, opt, repo, getenv)
	}
	o.Fatal("agentbus setup " + opt.Sub + ": not ported yet (busmon-installer-5..7); use ./setup.sh " + opt.Sub)
	return 1
}

// runInstall is setup.sh install's steps, in its order.
func runInstall(ctx context.Context, r setup.Runner, o *setup.Output, opt setup.Options, repo string, getenv func(string) string) int {
	missing, docker := setup.Preflight(ctx, r, o, opt, goModVersion(repo), runtime.GOOS, runtime.GOARCH)
	if missing > 0 && !opt.DryRun {
		o.Fatal(itoa(missing) + " required tool(s) missing; install them and re-run.")
		return 1
	}
	tmp, err := os.MkdirTemp("", "agentbus-setup-")
	if err != nil {
		o.Fatal(err.Error())
		return 1
	}
	defer os.RemoveAll(tmp)
	in := &setup.Installer{R: r, O: o, Opt: opt, Repo: repo, Tmp: tmp, Getenv: getenv, Now: time.Now, Setenv: os.Setenv}
	o.Step("2/5 Checking herdr workspace manager")
	in.CheckHerdr(ctx)
	o.Step("3/5 Building and installing agentbus & busmon, linking the herdr plugin")
	in.Rev = in.RepoRev(ctx)
	if !opt.DryRun {
		if err := os.MkdirAll(opt.BinDir, 0o755); err != nil {
			o.Fatal(err.Error())
			return 1
		}
	}
	for _, b := range [][2]string{{"agentbus", "./cmd/agentbus"}, {"busmon", "./cmd/busmon"}} {
		if err := in.BuildAndInstall(ctx, b[0], b[1]); err != nil {
			return 1
		}
	}
	in.RoleScriptLinks()
	in.KeyBinding(ctx, in.LinkPlugin(ctx))
	in.ServicesHint(ctx)
	in.CheckPath(getenv("PATH"))
	in.ExternalSkills(ctx)
	in.RoleSkills(ctx)
	o.Step("4/5 Checking Redis broker service")
	in.Redis(ctx, docker)
	in.Clients()
	var tty io.ReadWriter
	if t := setup.OpenTTY(); t != nil {
		defer t.Close()
		tty = t
	}
	in.Identity(ctx, &setup.Prompter{Yes: opt.Yes, TTY: tty})
	in.Models(ctx)
	return o.Summary(opt, missing, in.Rev)
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
