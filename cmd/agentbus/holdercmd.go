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
	"syscall"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/rolecfg"
	"github.com/netbja/agent-bus-monitor/service"
)

// mainHolder is `agentbus --project P holder [--env <file>]`, the command a
// holder unit runs (spec 2026-11-10-herdr-exit-design.md, 4.4): the project's
// env file first, as `service run` loads it, because the holder starts every
// agent and a service manager gives it a minimal PATH; then the launcher and
// the manifest from that environment, and the working directory the unit
// set. It makes no broker connection (4.4). An invalid configuration exits 0:
// the manager must not restart what cannot work (`agentbus up` rewrites it).
func mainHolder(project string, args []string, stderr io.Writer) int {
	cfg, err := holderConfig(project, args)
	if err != nil {
		fmt.Fprintf(stderr, "holder: invalid configuration: %v; not restarting\n", err)
		return 0
	}
	if paths, err := service.PathsFrom(os.Getenv); err == nil {
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

// holderConfig loads the env file into this process's environment (the
// agents inherit it) and builds the holder's Config from it.
func holderConfig(project string, args []string) (holder.Config, error) {
	if !bus.ValidName(project) {
		return holder.Config{}, fmt.Errorf("invalid project %q", project)
	}
	envFile := ""
	switch {
	case len(args) == 0:
	case len(args) == 2 && args[0] == "--env":
		envFile = args[1]
	default:
		return holder.Config{}, errors.New("usage: agentbus --project <p> holder [--env <file>]")
	}
	if envFile == "" {
		paths, err := service.PathsFrom(os.Getenv)
		if err != nil {
			return holder.Config{}, err
		}
		envFile = paths.EnvFile(project)
	}
	env, err := service.LoadEnvFile(envFile)
	if err != nil {
		return holder.Config{}, err
	}
	for k, v := range env {
		if err := os.Setenv(k, v); err != nil {
			return holder.Config{}, err
		}
	}
	repo, err := repoDir(os.Getenv)
	if err != nil {
		return holder.Config{}, err
	}
	launcher := filepath.Join(repo, "scripts", "agent-launch")
	if st, err := os.Stat(launcher); err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
		return holder.Config{}, fmt.Errorf("no executable launcher at %s", launcher)
	}
	manifest, err := rolecfg.ManifestPath(os.Getenv)
	if err != nil {
		return holder.Config{}, err
	}
	if _, err := rolecfg.Load(manifest); err != nil {
		return holder.Config{}, fmt.Errorf("the role manifest: %w", err)
	}
	dir, err := os.Getwd()
	if err != nil {
		return holder.Config{}, err
	}
	build := holder.BuildID()
	sock, err := holder.SocketPath(os.Getenv, project, service.BuildKey(build))
	if err != nil {
		return holder.Config{}, err
	}
	r := config.Resolver{Getenv: os.Getenv, Files: []*config.File{config.Load(config.Path(os.Getenv), "config.toml")}}
	launch := holder.TemplateLaunch{Project: project, Launcher: launcher, Manifest: manifest, Dir: dir}
	return holder.Config{
		Project:     project,
		Build:       build,
		Socket:      sock,
		Launch:      launch.Command,
		Scrollback:  r.Int("holder.scrollback"),
		SubmitWait:  r.Duration("dispatch.submit_wait"),
		PersonQuiet: r.Duration("dispatch.person_quiet"),
	}, nil
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
