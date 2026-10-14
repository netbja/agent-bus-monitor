// Package service runs a project's dispatch and missions loops as user
// services under the operating system's own manager: systemd --user on Linux,
// launchd on macOS. It writes the units, the environment file the services
// read, rotates their logs and keeps the restart budget; it never supervises a
// process itself (v0.12, docs/superpowers/specs/2026-10-14-project-services-design.md).
package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The service kinds: the wake dispatcher and the missions conductor.
const (
	Dispatch = "dispatch"
	Missions = "missions"
)

// Kinds is every kind, in start order.
var Kinds = []string{Dispatch, Missions}

// ValidKind reports whether k is a service kind.
func ValidKind(k string) bool { return k == Dispatch || k == Missions }

// Paths are the per-user directories, resolved once from the environment.
type Paths struct {
	Home, ConfigHome, StateHome, DataHome string
}

// PathsFrom resolves the XDG directories, with their usual defaults.
func PathsFrom(getenv func(string) string) (Paths, error) {
	home := getenv("HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("no home directory: %w", err)
		}
		home = h
	}
	or := func(k, def string) string {
		if v := getenv(k); v != "" && filepath.IsAbs(v) {
			return v
		}
		return filepath.Join(home, def)
	}
	return Paths{Home: home, ConfigHome: or("XDG_CONFIG_HOME", ".config"),
		StateHome: or("XDG_STATE_HOME", ".local/state"), DataHome: or("XDG_DATA_HOME", ".local/share")}, nil
}

// EnvFile is the project's service environment (0600): the broker
// connection and herdr session the services use.
func (p Paths) EnvFile(project string) string {
	return filepath.Join(p.ConfigHome, "agentbus", "services", project+".env")
}

// StateDir is the project's state directory, shared with the conductor.
func (p Paths) StateDir(project string) string {
	return filepath.Join(p.StateHome, "agentbus", project)
}

// LogFile is where a service logs (rotated).
func (p Paths) LogFile(project, kind string) string {
	return filepath.Join(p.StateDir(project), kind+".log")
}

// BudgetFile holds a service's recent failed starts.
func (p Paths) BudgetFile(project, kind string) string {
	return filepath.Join(p.StateDir(project), kind+".restarts")
}

// UnitName is the systemd unit of a project's service.
func UnitName(project, kind string) string { return "agentbus-" + project + "-" + kind + ".service" }

// Label is the launchd label of a project's service.
func Label(project, kind string) string { return "dev.agentbus." + project + "." + kind }

// parseUnitName splits "agentbus-<project>-<kind>.service"; the project may
// itself hold dashes, the kind is the last one.
func parseUnitName(name string) (project, kind string, ok bool) {
	s, found := strings.CutPrefix(name, "agentbus-")
	if !found {
		return "", "", false
	}
	if s, found = strings.CutSuffix(s, ".service"); !found {
		return "", "", false
	}
	i := strings.LastIndexByte(s, '-')
	if i <= 0 || !ValidKind(s[i+1:]) {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

// parseLabel splits "dev.agentbus.<project>.<kind>"; a project name holds no dot.
func parseLabel(label string) (project, kind string, ok bool) {
	s, found := strings.CutPrefix(label, "dev.agentbus.")
	if !found {
		return "", "", false
	}
	project, kind, found = strings.Cut(s, ".")
	if !found || project == "" || !ValidKind(kind) {
		return "", "", false
	}
	return project, kind, true
}
