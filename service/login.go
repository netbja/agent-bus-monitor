package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Start at login (self-repair step 9, docs/superpowers/specs/2026-11-09-self-repair-watcher-design.md
// 4.4): the option services.start_at_login, off by default, starts a
// project's services when the person logs in, so the self-repair watcher
// covers a reboot as soon as herdr opens the workspace. No linger: without a
// session there is no workspace to relaunch into. One backend per platform;
// elsewhere the option is refused with the reason.

// LoginStart enables or disables a project's services at the person's login.
type LoginStart interface {
	Enable(project string) error
	Disable(project string) error
	Enabled(project string) (bool, error)
}

// NoLoginStart is why the option cannot be honoured without a backend.
const NoLoginStart = "no service manager to start at login"

// LoginStartOf is m's login backend, or nil and why there is none.
func LoginStartOf(m Manager) (LoginStart, string) {
	if ls, ok := m.(LoginStart); ok {
		return ls, ""
	}
	return nil, NoLoginStart
}

// projectUnits are the kinds of project installed under m, in start order.
func projectUnits(m Manager, project string) ([]string, error) {
	units, err := m.List()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, k := range Kinds {
		for _, u := range units {
			if u.Project == project && u.Kind == k {
				out = append(out, k)
			}
		}
	}
	return out, nil
}

func noServices(project string) error {
	return fmt.Errorf("no service installed for %s: run agentbus up %s first", project, project)
}

// systemdInstall is the section a unit carries only while the option is on.
const systemdInstall = "\n[Install]\nWantedBy=default.target\n"

// Enable gives each of the project's units its [Install] section and enables
// it (a wants link from default.target); it starts nothing now.
func (m Systemd) Enable(project string) error {
	kinds, err := projectUnits(m, project)
	if err != nil {
		return err
	}
	if len(kinds) == 0 {
		return noServices(project)
	}
	for _, k := range kinds {
		if err := m.setInstall(project, k, true); err != nil {
			return err
		}
		if err := m.ctl("enable", UnitName(project, k)); err != nil {
			return err
		}
	}
	return nil
}

// Disable disables each of the project's units and removes the section; a
// running service keeps running.
func (m Systemd) Disable(project string) error {
	kinds, err := projectUnits(m, project)
	if err != nil {
		return err
	}
	for _, k := range kinds {
		if err := m.ctl("disable", UnitName(project, k)); err != nil {
			return err
		}
		if err := m.setInstall(project, k, false); err != nil {
			return err
		}
	}
	return nil
}

// isEnabledStates are the answers systemctl is-enabled prints; it exits
// non-zero for most of them, so the answer is read from the output.
var isEnabledStates = map[string]bool{"enabled": true, "enabled-runtime": true, "linked": true, "linked-runtime": true,
	"alias": true, "masked": true, "masked-runtime": true, "static": true, "indirect": true, "disabled": true,
	"generated": true, "transient": true, "bad": true, "not-found": true}

// startsAtLogin are the states whose unit a login may start.
var startsAtLogin = map[string]bool{"enabled": true, "enabled-runtime": true, "linked": true, "linked-runtime": true}

// unitState is is-enabled's answer for one unit. An answer that is no state
// is an error, never "disabled": a failed read must not let a wants link
// survive the option going off (reviewer-codex).
func (m Systemd) unitState(project, kind string) (string, error) {
	out, err := m.Run("--user", "is-enabled", UnitName(project, kind))
	state := strings.TrimSpace(string(out))
	if !isEnabledStates[state] {
		return "", fmt.Errorf("systemctl --user is-enabled %s: %s", UnitName(project, kind), firstLine(out, err))
	}
	return state, nil
}

// Enabled: some installed unit of the project starts at login (none
// installed: false). A partly enabled project counts, so the option going
// off disables every unit (foureyes: an Enable that failed half way, a unit
// disabled by hand).
func (m Systemd) Enabled(project string) (bool, error) {
	kinds, err := projectUnits(m, project)
	if err != nil {
		return false, err
	}
	for _, k := range kinds {
		state, err := m.unitState(project, k)
		if err != nil {
			return false, err
		}
		if startsAtLogin[state] {
			return true, nil
		}
	}
	return false, nil
}

// mayStartAtLogin: is-enabled says a login may start the unit, or cannot
// say. Its state, not the [Install] section, is the fact: a link outlives
// the section (foureyes).
func (m Systemd) mayStartAtLogin(project, kind string) bool {
	state, err := m.unitState(project, kind)
	return err != nil || startsAtLogin[state]
}

// setInstall adds or removes the [Install] section of an installed unit and
// reloads the manager when the file changed.
func (m Systemd) setInstall(project, kind string, on bool) error {
	p := m.path(project, kind)
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	base := strings.TrimSuffix(string(b), systemdInstall)
	text := base
	if on {
		text += systemdInstall
	}
	changed, err := writeIfChanged(p, text)
	if err != nil || !changed {
		return err
	}
	return m.ctl("daemon-reload")
}

// agentPath is the job's copy in ~/Library/LaunchAgents, which launchd loads
// at login.
func (m Launchd) agentPath(project, kind string) string {
	return filepath.Join(m.Paths.Home, "Library", "LaunchAgents", Label(project, kind)+".plist")
}

// errForeign: a LaunchAgents file of the job's label that agentbus did not
// write is never overwritten or removed.
var errForeign = errors.New("a LaunchAgents file of this label was not written by agentbus: left as it is")

// ours reports whether path is absent (false, nil) or a plist agentbus wrote.
func ours(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !strings.Contains(string(b), launchdMark) {
		return false, fmt.Errorf("%s: %w", path, errForeign)
	}
	return true, nil
}

// Enable copies each of the project's job plists into ~/Library/LaunchAgents
// (RunAtLoad starts it at login); it loads nothing now.
func (m Launchd) Enable(project string) error {
	kinds, err := projectUnits(m, project)
	if err != nil {
		return err
	}
	if len(kinds) == 0 {
		return noServices(project)
	}
	for _, k := range kinds {
		if err := m.writeAgent(project, k); err != nil {
			return err
		}
	}
	return nil
}

func (m Launchd) writeAgent(project, kind string) error {
	if _, err := ours(m.agentPath(project, kind)); err != nil {
		return err
	}
	b, err := os.ReadFile(m.path(project, kind))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.agentPath(project, kind)), 0o755); err != nil {
		return err
	}
	_, err = writeIfChanged(m.agentPath(project, kind), string(b))
	return err
}

// Disable removes the LaunchAgents copies; a loaded job keeps running.
func (m Launchd) Disable(project string) error {
	for _, k := range Kinds {
		if err := m.removeAgent(project, k); err != nil {
			return err
		}
	}
	return nil
}

// removeAgent removes the job's LaunchAgents copy; a file agentbus did not
// write is left as it is, silently: it is the person's, not ours to clean.
func (m Launchd) removeAgent(project, kind string) error {
	mine, err := ours(m.agentPath(project, kind))
	if errors.Is(err, errForeign) {
		return nil
	}
	if err != nil || !mine {
		return err
	}
	return os.Remove(m.agentPath(project, kind))
}

// Enabled: some job of the project has its LaunchAgents copy (a partly
// enabled project counts, as for systemd).
func (m Launchd) Enabled(project string) (bool, error) {
	for _, k := range Kinds {
		mine, err := ours(m.agentPath(project, k))
		if errors.Is(err, errForeign) {
			continue // the person's file, not ours to report or remove
		}
		if err != nil || mine {
			return mine, err
		}
	}
	return false, nil
}
