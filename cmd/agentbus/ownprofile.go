package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// tabLaunch reads a template tab's agent-launch command: the manifest it
// names (ROLES_TOML=...), the role after agent-launch and the --profile after
// it. role is "" when the tab launches no agent. The template quotes its
// words (rolecfg.ShellJoin): they are split back as a shell would, so a path
// with a space stays one word (reviewer-codex).
func tabLaunch(command string) (manifest, role, profile string) {
	words, err := rolecfg.ShellSplit(command)
	if err != nil {
		words = strings.Fields(strings.ReplaceAll(command, "'", ""))
	}
	at := -1
	for i, w := range words {
		if m, ok := strings.CutPrefix(w, "ROLES_TOML="); ok {
			manifest = m
		}
		if at < 0 && filepath.Base(w) == "agent-launch" && i+1 < len(words) {
			at, role = i, words[i+1]
		}
	}
	for i := at + 2; at >= 0 && i+1 < len(words); i++ {
		if words[i] == "--profile" {
			profile = words[i+1]
		}
	}
	return manifest, role, profile
}

// launchProfile resolves the profile a tab names for role: none is the
// role's own in roles.toml; another must be that one or one of its
// fallback_profiles (spec "A role's own profile in a project"), so a template
// never runs a role on a profile roles.toml no longer allows.
func launchProfile(m *rolecfg.Manifest, role, profile string) (rolecfg.Profile, error) {
	own, err := m.Resolve(role)
	if err != nil || profile == "" || profile == own.Profile {
		return own, err
	}
	if !slices.Contains(m.Fallbacks(role), profile) {
		return rolecfg.Profile{}, fmt.Errorf("profile %s is not %s's own nor one of its fallback_profiles in roles.toml", profile, role)
	}
	return m.ResolveWith(role, profile)
}

// ownProfile is role's own profile in project: the --profile of the role's
// tab in the project's template (rolecfg.ProjectsDir), else roles.toml's
// (no template, no tab for the role as for a pop, no --profile). A template
// that exists but cannot be read is an error, never roles.toml's in silence.
// Every reader that compares against a role's own profile (doctor, swap,
// reload) reads it here.
func ownProfile(m *rolecfg.Manifest, env func(string) string, project, role string) (rolecfg.Profile, error) {
	path := filepath.Join(rolecfg.ProjectsDir(env), project+".toml")
	var tpl upTemplate
	if _, err := toml.DecodeFile(path, &tpl); errors.Is(err, os.ErrNotExist) {
		return m.Resolve(role)
	} else if err != nil {
		return rolecfg.Profile{}, fmt.Errorf("reading %s: %v", path, err)
	}
	for _, t := range tpl.Tabs {
		if _, r, profile := tabLaunch(t.Command); r == role {
			return launchProfile(m, role, profile)
		}
	}
	return m.Resolve(role)
}

// checkProfiles stops up before any service, workspace, tab or pane when an
// agent tab names a profile roles.toml no longer allows: started, it would
// run unchecked by checkClients and the trust gate. Its own step, not behind
// checkClients' lookups (foureyes). A tab that launches no agent, or a role
// roles.toml does not have, keeps today's skip (agent-launch refuses it).
func checkProfiles(d upDeps, tpl upTemplate, path string, stderr io.Writer) bool {
	for _, t := range tpl.Tabs {
		if _, _, err := tabProfile(t.Command, d.Env); err != nil {
			fmt.Fprintf(stderr, "agentbus up: tab %s: %v: choose another in %s\n", t.Name, err, path)
			return false
		}
	}
	return true
}
