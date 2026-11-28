package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/netbja/agent-bus-monitor/cgroot"
	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/holder"
	"github.com/netbja/agent-bus-monitor/service"
)

// holderScopeDeps are what holderScopes reads from the host (fakes in tests).
type holderScopeDeps struct {
	goos    string
	look    func(string) (string, error)
	self    string // this build's binary: agent-enter
	getenv  func(string) string
	answers func(project, key string) bool // project's holder of build key answers on its socket
	manager func() (service.HolderManager, error)
}

func liveHolderScopeDeps(getenv func(string) string) holderScopeDeps {
	self, _ := os.Executable()
	return holderScopeDeps{goos: runtime.GOOS, look: exec.LookPath, self: self, getenv: getenv,
		answers: func(project, key string) bool { _, err := holder.Terms(project, key); return err == nil },
		manager: func() (service.HolderManager, error) {
			m, err := service.Local("linux", service.CommandRunner(service.ManagerBin("linux", getenv)), getenv)
			if err != nil {
				return nil, err
			}
			hm, ok := m.(service.HolderManager)
			if !ok {
				return nil, errors.New("the service manager holds no holder units")
			}
			return hm, nil
		}}
}

// newHolderScopes is mainHolder's (a test's holder process turns it off:
// no test reaches the host's user manager).
var newHolderScopes = func(project string, getenv func(string) string) (*holder.Scopes, string) {
	return holderScopes(project, liveHolderScopeDeps(getenv))
}

// holderScopes is the holder's scope launch (resource-limits §12.5, step
// 4b): on Linux with systemd-run and systemctl, each terminal in its own
// scope with cap B, read from config.toml at each launch. Without them, nil
// and why: the panes start as before, uncapped by B.
func holderScopes(project string, d holderScopeDeps) (*holder.Scopes, string) {
	if d.goos != "linux" {
		return nil, "not Linux"
	}
	if d.self == "" {
		return nil, "this binary's path is unknown (agent-enter)"
	}
	run, err := d.look("systemd-run")
	if err != nil {
		return nil, "no systemd-run: " + err.Error()
	}
	ctl, err := d.look("systemctl")
	if err != nil {
		return nil, "no systemctl: " + err.Error()
	}
	us := cgroot.UserScopes{Systemctl: ctl}
	return &holder.Scopes{
		Argv: func(unit string, argv []string) []string {
			r := config.Machine(d.getenv)
			limit := func(name string) string {
				if v := r.String(name); v != "off" {
					return v
				}
				return ""
			}
			return cgroot.ScopeArgv(cgroot.ScopeOpts{SystemdRun: run, Unit: unit, MemoryMax: limit("limits.agent_memory_max"),
				SwapMax: limit("limits.agent_swap_max"), Enter: []string{d.self, "agent-enter"}, Argv: argv})
		},
		List: func() ([]string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			return us.List(ctx, project)
		},
		Stop: func(units ...string) error {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			return us.Stop(ctx, units...)
		},
		// A supervised holder has no unit, so its socket answering is
		// enough; else its unit; a manager that cannot say is an error.
		Active: func(key string) (bool, error) {
			if d.answers(project, key) {
				return true, nil
			}
			hm, err := d.manager()
			if err != nil {
				return false, err
			}
			return hm.HolderActive(project, key)
		},
	}, ""
}
