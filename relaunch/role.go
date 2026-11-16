package relaunch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/BurntSushi/toml"

	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// ErrBusy: another up of the project (or the watcher) holds the lock.
var ErrBusy = errors.New("another agentbus up of this project is running")

// ProjectLock takes ~/.cache/agentbus/up-<project>.lock: one up per project at a
// time, so two ups never split or fill one workspace together. wait=false
// gives up with ErrBusy while another holds it. Any failure to establish
// the lock is an error: up then touches no pane.
func ProjectLock(cacheDir string) func(project string, wait bool) (func(), error) {
	return func(project string, wait bool) (func(), error) {
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			return nil, fmt.Errorf("lock directory: %w", err)
		}
		f, err := os.OpenFile(filepath.Join(cacheDir, "up-"+project+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("lock file: %w", err)
		}
		how := syscall.LOCK_EX
		if !wait {
			how |= syscall.LOCK_NB
		}
		if err := syscall.Flock(int(f.Fd()), how); err != nil {
			f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				return nil, ErrBusy
			}
			return nil, fmt.Errorf("lock %s: %w", f.Name(), err)
		}
		return func() {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			f.Close()
		}, nil
	}
}

// CacheDir is ~/.cache/agentbus (or $XDG_CACHE_HOME/agentbus): where the
// project lock lives.
func CacheDir(env func(string) string) string {
	if x := env("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "agentbus")
	}
	return filepath.Join(env("HOME"), ".cache", "agentbus")
}

// ReadTemplate reads project's template from the projects directory.
func ReadTemplate(env func(string) string, project string) (Template, error) {
	var tpl Template
	if _, err := toml.DecodeFile(filepath.Join(rolecfg.ProjectsDir(env), project+".toml"), &tpl); err != nil {
		return Template{}, fmt.Errorf("template: %w", err)
	}
	return tpl, nil
}
