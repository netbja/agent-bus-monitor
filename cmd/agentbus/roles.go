package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/netbja/agent-bus-monitor/buildinfo"
	"github.com/netbja/agent-bus-monitor/dispatch"
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
// AGENTBUS_REPO, else the checkout setup.sh stamped (buildinfo.Repo) when it
// holds a roles.toml, else this binary's directory (the scripts run
// <repo>/agentbus).
func manifestPath(env []string) (string, error) {
	path, ok := lookupEnv(env, "ROLES_TOML")
	if !ok || path == "" {
		repo, ok := lookupEnv(env, "AGENTBUS_REPO")
		if (!ok || repo == "") && buildinfo.Repo != "" {
			// The checkout setup.sh stamped, as repoDir finds scripts/bootstrap:
			// an installed binary has no roles.toml beside it.
			if st, err := os.Stat(filepath.Join(buildinfo.Repo, "roles.toml")); err == nil && !st.IsDir() {
				repo, ok = buildinfo.Repo, true
			}
		}
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
	if action == "projects-dir" { // needs no manifest: bootstrap asks before any exists
		fmt.Fprintln(stdout, rolecfg.ProjectsDir(func(k string) string { v, _ := lookupEnv(env, k); return v }))
		return nil
	}
	switch action { // Claude's SessionStart hook and its sender: silent, never failing
	case "report-resume":
		reportResume(rest, env, os.Stdin, stdout, stderr, spawnDetached,
			hookFacts{Ancestors: func() []dispatch.ProcID { return dispatch.Ancestors(os.Getpid()) }, Now: time.Now})
		return nil
	case "report-resume-send":
		reportResumeSend(rest, defaultSenderDeps())
		return nil
	case "prove-session":
		proveSessionSend(rest, openProver, dispatch.ProcStart)
		return nil
	}
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
		switch {
		case len(rest) == 2:
			return rolecfg.Launch(m, rest[0], rest[1], env, stdout, stderr, syscall.Exec)
		case len(rest) == 4 && rest[2] == "--profile":
			return rolecfg.LaunchProfile(m, rest[0], rest[1], rest[3], env, stdout, stderr, syscall.Exec)
		case len(rest) == 4 && rest[2] == "--resume":
			return rolecfg.LaunchResume(m, rest[0], rest[1], rest[3], env, stdout, stderr, syscall.Exec)
		case len(rest) == 6 && rest[2] == "--profile" && rest[4] == "--resume": // a swapped role resumed by herdr
			if rest[5] == "" {
				return errors.New("resume session id is required")
			}
			return rolecfg.LaunchProfileResume(m, rest[0], rest[1], rest[3], rest[5], env, stdout, stderr, syscall.Exec)
		}
		return errors.New("usage: agent-launch <role> <project> [--profile <profile>] [--resume <session-id>]")
	case "fallbacks":
		return rolesFallbacks(m, rest, stdout)
	case "draft":
		return rolesDraft(m, rest, env, stdout)
	case "skill-users":
		if len(rest) == 0 {
			return errors.New("usage: skill-users <skill>...")
		}
		for _, role := range m.RolesUsing(rest) {
			fmt.Fprintln(stdout, role)
		}
		return nil
	case "reviewers":
		return rolesReviewers(m, slices.Contains(rest, "--json"), stdout)
	case "install":
		if len(rest) == 1 && rest[0] == "--missing-ok" {
			return rolecfg.InstallSkillsMissingOK(m, env, stdout, stderr)
		}
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

// fallbackRow is one line of `roles fallbacks`.
type fallbackRow struct {
	Profile  string `json:"profile"`
	Client   string `json:"client"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Own      bool   `json:"own,omitempty"`
	Note     string `json:"note,omitempty"`
}

// reviewerRoles lose their model diversity on the coder's provider.
var reviewerRoles = map[string]bool{"foureyes": true, "security": true}

// rolesFallbacks prints a role's profile, then each fallback (v0.15 §4).
func rolesFallbacks(m *rolecfg.Manifest, rest []string, stdout io.Writer) error {
	asJSON := len(rest) == 2 && rest[1] == "--json"
	if len(rest) != 1 && !asJSON {
		return errors.New("usage: fallbacks <role> [--json]")
	}
	role := rest[0]
	if _, ok := m.Roles[role]; !ok {
		return errRoleMissing
	}
	own, err := m.Resolve(role)
	if err != nil {
		return err
	}
	coderProvider := ""
	if _, ok := m.Roles["coder"]; ok && role != "coder" {
		if c, err := m.Resolve("coder"); err == nil {
			coderProvider = c.Provider
		}
	}
	rows := []fallbackRow{{Profile: own.Profile, Client: own.Client, Provider: own.Provider, Model: own.Model, Own: true}}
	for _, name := range m.Fallbacks(role) {
		p, err := m.ResolveWith(role, name)
		if err != nil {
			return err
		}
		r := fallbackRow{Profile: name, Client: p.Client, Provider: p.Provider, Model: p.Model}
		if reviewerRoles[role] && coderProvider != "" && p.Provider == coderProvider {
			r.Note = "same provider as coder: the review loses its model diversity"
		}
		rows = append(rows, r)
	}
	if asJSON {
		return json.NewEncoder(stdout).Encode(rows)
	}
	for _, r := range rows {
		kind := "fallback"
		if r.Own {
			kind = "profile "
		}
		line := fmt.Sprintf("%s %-12s %-12s %-12s %s", kind, r.Profile, r.Client, r.Provider, r.Model)
		if r.Note != "" {
			line += "  (" + r.Note + ")"
		}
		fmt.Fprintln(stdout, line)
	}
	if len(rows) == 1 {
		fmt.Fprintln(stdout, "no fallback_profiles for "+role)
	}
	return nil
}
