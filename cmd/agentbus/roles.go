package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// errRoleMissing is the exit-3 answer of exists and field.
var errRoleMissing = errors.New("role missing")

// runRoles is the role manifest CLI the scripts call (agent-launch,
// link-role-skills.sh, lib/roles.sh, bootstrap): the Go port of
// scripts/lib/role_config.py. It needs no project and no broker. Exit codes
// are the Python ones: 0, 1 on error (agent roles: ... on stderr), 3 for a
// role that exists/field do not find.
func runRoles(args, env []string, stdout, stderr io.Writer) int {
	err := roles1(args, env, stdout, stderr)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errRoleMissing):
		return 3
	}
	fmt.Fprintln(stderr, "agent roles: "+err.Error())
	return 1
}

func lookupEnv(env []string, key string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}

// manifestPath is ROLES_TOML, else <repo>/roles.toml, where the repo is
// AGENTBUS_REPO or this binary's directory (the scripts run <repo>/agentbus).
func manifestPath(env []string) (string, error) {
	path, ok := lookupEnv(env, "ROLES_TOML")
	if !ok || path == "" {
		repo, ok := lookupEnv(env, "AGENTBUS_REPO")
		if !ok || repo == "" {
			exe, err := os.Executable()
			if err != nil {
				return "", err
			}
			if r, err := filepath.EvalSymlinks(exe); err == nil {
				exe = r
			}
			repo = filepath.Dir(exe)
		}
		path = filepath.Join(repo, "roles.toml")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	return abs, nil
}

func roles1(args, env []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("expected action: launch, install, validate, exists, tier, field, template, check-toml")
	}
	action, rest := args[0], args[1:]
	if action == "check-toml" {
		if len(rest) != 1 {
			return errors.New("usage: check-toml <file>")
		}
		var v map[string]interface{}
		_, err := toml.DecodeFile(rest[0], &v)
		return err
	}
	path, err := manifestPath(env)
	if err != nil {
		return err
	}
	m, err := rolecfg.Load(path)
	if err != nil {
		return err
	}
	switch action {
	case "launch":
		if len(rest) != 2 {
			return errors.New("usage: agent-launch <role> <project>")
		}
		return rolecfg.Launch(m, rest[0], rest[1], env, stdout, stderr, syscall.Exec)
	case "install":
		return rolecfg.InstallSkills(m, env, stdout)
	case "validate":
		for _, tier := range []string{"boot", "pop"} {
			for _, role := range m.RolesByTier(tier) {
				if _, err := m.Resolve(role); err != nil {
					return err
				}
			}
		}
		return nil
	case "exists":
		if len(rest) != 1 {
			return errors.New("usage: exists <role>")
		}
		if _, ok := m.Roles[rest[0]]; !ok {
			return errRoleMissing
		}
		return nil
	case "tier":
		if len(rest) != 1 {
			return errors.New("usage: tier <boot|pop>")
		}
		for _, role := range m.RolesByTier(rest[0]) {
			fmt.Fprintln(stdout, role)
		}
		return nil
	case "field":
		if len(rest) != 2 {
			return errors.New("usage: field <role> <name>")
		}
		if _, ok := m.Roles[rest[0]]; !ok {
			return errRoleMissing
		}
		values, ok, err := m.Field(rest[0], rest[1])
		if err != nil || !ok {
			return err
		}
		fmt.Fprintln(stdout, strings.Join(values, "\n"))
		return nil
	case "template":
		if len(rest) != 6 && len(rest) != 7 {
			return errors.New("usage: template <project> <cwd> <scripts> <repo> <0|1 dispatch> <0|1 missions> [<busmon --as name>]")
		}
		as := ""
		if len(rest) == 7 {
			as = rest[6]
		}
		fmt.Fprint(stdout, rolecfg.Template(m, path, rest[0], rest[1], rest[2], rest[3], as, rest[4] == "1", rest[5] == "1"))
		return nil
	}
	return errors.New("unknown action")
}
