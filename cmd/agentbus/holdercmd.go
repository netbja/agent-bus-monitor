package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
)

// mainHolder is `agentbus --project P holder [--env <file>]`, the command a
// holder unit runs (spec 2026-11-10-herdr-exit-design.md, 4.4): it reads the
// project's env file (never into its own environment, see holderConfig),
// because the holder starts every agent and a service manager gives it a
// minimal PATH; each agent gets the file's current variables, launcher,
// manifest and directory. It makes no broker connection (4.4). An invalid configuration exits 0:
// the manager must not restart what cannot work (`agentbus up` rewrites it).
func mainHolder(project string, args []string, stderr io.Writer) int {
	cfg, getenv, err := holderConfig(project, args)
	if err != nil {
		fmt.Fprintf(stderr, "holder: invalid configuration: %v; not restarting\n", err)
		return 0
	}
	if paths, err := service.PathsFrom(getenv); err == nil {
		crash := paths.CrashFile(project, service.Holder+"."+service.BuildKey(cfg.Build))
		if os.MkdirAll(filepath.Dir(crash), 0o700) == nil {
			if cf, err := os.OpenFile(crash, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				_ = debug.SetCrashOutput(cf, debug.CrashOptions{})
				cf.Close()
			}
		}
	}
	srv, err := holder.NewServer(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "holder: %v; not restarting\n", err)
		return 0
	}
	if err := srv.Listen(); err != nil {
		fmt.Fprintf(stderr, "holder: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(stderr, "holder: project %s, build %s, socket %s\n", project, cfg.Build, cfg.Socket)
	err = srv.Serve(ctx)
	srv.Shutdown()
	if err != nil {
		fmt.Fprintf(stderr, "holder: %v\n", err)
		return 1
	}
	return 0
}

// holderConfig builds the holder's Config, and the environment it reads its
// socket, settings and directories through: the env file over the holder's
// own. The file is never written into this process's environment: the
// agents inherit that, and a variable later removed from the file (a secret)
// would still reach them (reviewer-codex). Launch reads the launch context
// afresh at each Start (holderLaunch).
func holderConfig(project string, args []string) (holder.Config, func(string) string, error) {
	if !bus.ValidName(project) {
		return holder.Config{}, nil, fmt.Errorf("invalid project %q", project)
	}
	envFile := ""
	switch {
	case len(args) == 0:
	case len(args) == 2 && args[0] == "--env":
		envFile = args[1]
	default:
		return holder.Config{}, nil, errors.New("usage: agentbus --project <p> holder [--env <file>]")
	}
	if envFile == "" {
		paths, err := service.PathsFrom(os.Getenv)
		if err != nil {
			return holder.Config{}, nil, err
		}
		envFile = paths.EnvFile(project)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return holder.Config{}, nil, err
	}
	l := holderLaunch{project: project, envFile: envFile, cwd: cwd, base: map[string]string{}}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			l.base[k] = v
		}
	}
	env, _, err := l.context() // the configuration is checked once before serving
	if err != nil {
		return holder.Config{}, nil, err
	}
	getenv := func(k string) string {
		if v, ok := env[k]; ok {
			return v
		}
		return l.base[k]
	}
	build := holder.BuildID()
	sock, err := holder.SocketPath(getenv, project, service.BuildKey(build))
	if err != nil {
		return holder.Config{}, nil, err
	}
	r := config.Resolver{Getenv: getenv, Files: []*config.File{config.Load(config.Path(getenv), "config.toml")}}
	return holder.Config{
		Project:     project,
		Build:       build,
		Socket:      sock,
		Launch:      l.command,
		Scrollback:  r.Int("holder.scrollback"),
		SubmitWait:  r.Duration("dispatch.submit_wait"),
		PersonQuiet: r.Duration("dispatch.person_quiet"),
	}, getenv, nil
}

// holderLaunch turns a Start into a command with the project's launch context
// as it is now. `agentbus up` may rewrite the env file and the working
// directory while this build's holder runs, and the holder is never restarted
// for it (that would kill its agents): so each Start reads the env file again
// and takes from it the PATH, the launcher (service.HolderRepoEnv, else the
// checkout repoDir finds), the manifest (service.HolderManifestEnv, else
// rolecfg.ManifestPath) and the directory (service.HolderDirEnv, else the
// unit's); the new agent gets the file's variables. A running terminal keeps its own. A context
// that cannot be read refuses the Start, never falls back to the old one.
type holderLaunch struct {
	project, envFile, cwd string
	base                  map[string]string // the holder's environment before any env file
}

// context is the env file's variables and the TemplateLaunch they give.
func (l holderLaunch) context() (map[string]string, holder.TemplateLaunch, error) {
	env, err := service.LoadEnvFile(l.envFile)
	if err != nil {
		return nil, holder.TemplateLaunch{}, err
	}
	getenv := func(k string) string {
		if v, ok := env[k]; ok {
			return v
		}
		return l.base[k]
	}
	repo := env[service.HolderRepoEnv]
	if repo == "" {
		if repo, err = repoDir(getenv); err != nil {
			return nil, holder.TemplateLaunch{}, err
		}
	}
	launcher := filepath.Join(repo, "scripts", "agent-launch")
	if st, err := os.Stat(launcher); err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
		return nil, holder.TemplateLaunch{}, fmt.Errorf("no executable launcher at %s", launcher)
	}
	manifest := env[service.HolderManifestEnv]
	if manifest == "" { // the launcher's own rule, its repo the one above
		withRepo := func(k string) string {
			if k == "AGENTBUS_REPO" && getenv(k) == "" {
				return repo
			}
			return getenv(k)
		}
		if manifest, err = rolecfg.ManifestPath(withRepo); err != nil {
			return nil, holder.TemplateLaunch{}, err
		}
	}
	if _, err := rolecfg.Load(manifest); err != nil {
		return nil, holder.TemplateLaunch{}, fmt.Errorf("the role manifest: %w", err)
	}
	dir := l.cwd
	if d := env[service.HolderDirEnv]; d != "" {
		dir = d
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() || !filepath.IsAbs(dir) {
		return nil, holder.TemplateLaunch{}, fmt.Errorf("the working directory %q is not an absolute directory", dir)
	}
	return env, holder.TemplateLaunch{Project: l.project, Launcher: launcher, Manifest: manifest, Dir: dir}, nil
}

func (l holderLaunch) command(s holder.Start) (holder.Command, error) {
	env, tl, err := l.context()
	if err != nil {
		return holder.Command{}, fmt.Errorf("holder: the launch context: %w", err)
	}
	c, err := tl.Command(s)
	if err != nil {
		return holder.Command{}, err
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	fresh := make([]string, 0, len(env)+len(c.Env))
	for _, k := range keys {
		fresh = append(fresh, k+"="+env[k])
	}
	// After the holder's own environment (which never holds the file's): the
	// current file's variables win, and one removed from it is not passed.
	c.Env = append(fresh, c.Env...)
	return c, nil
}

// holderTerms counts the terminals of project's holder of build key, asked
// over its socket; an error when none answers.
func holderTerms(project, key string) (int, error) {
	sock, err := holder.SocketPath(os.Getenv, project, key)
	if err != nil {
		return 0, err
	}
	cl, err := holder.Dial(sock, holder.BuildID())
	if err != nil {
		return 0, fmt.Errorf("no holder answers at %s: %w", sock, err)
	}
	defer cl.Close()
	r, err := cl.Call(holder.List{})
	if err != nil {
		return 0, err
	}
	return len(r.Terms), nil
}
