package service

import (
	"errors"
	"os"
	"path/filepath"
)

// The holder at login (holder hardening H1, spec
// 2026-11-17-holder-hardening-design.md §3): when services.start_at_login is
// on, the current build's holder unit starts at login with the project's
// other services, so the team's terminals are there when the dispatcher is.
// Only the current build: an older one drains and is never started again,
// so the enable follows the build. Nothing is started or stopped here.

// HolderLoginStart enables a project's holder at login.
type HolderLoginStart interface {
	// EnableHolder enables build key's holder unit and disables every
	// other build's of the project.
	EnableHolder(project, key string) error
	// DisableHolders disables every holder unit of the project.
	DisableHolders(project string) error
}

func (m Systemd) EnableHolder(project, key string) error {
	units, err := m.HolderUnits(project)
	if err != nil {
		return err
	}
	found := false
	for _, u := range units {
		if u.Key == key {
			found = true
			continue
		}
		if err := m.disableHolder(project, u.Key); err != nil {
			return err
		}
	}
	if !found {
		return errors.New("the holder unit of build " + key + " is not installed")
	}
	if err := m.setHolderInstall(project, key, true); err != nil {
		return err
	}
	return m.ctl("enable", HolderUnitName(project, key))
}

func (m Systemd) DisableHolders(project string) error {
	units, err := m.HolderUnits(project)
	if err != nil {
		return err
	}
	for _, u := range units {
		if err := m.disableHolder(project, u.Key); err != nil {
			return err
		}
	}
	return nil
}

func (m Systemd) disableHolder(project, key string) error {
	if err := m.ctl("disable", HolderUnitName(project, key)); err != nil {
		return err
	}
	return m.setHolderInstall(project, key, false)
}

// setHolderInstall is setInstall for a holder unit.
func (m Systemd) setHolderInstall(project, key string, on bool) error {
	return m.setInstallAt(m.holderPath(project, key), on)
}

func (m Launchd) holderAgentPath(project, key string) string {
	return filepath.Join(m.Paths.Home, "Library", "LaunchAgents", HolderLabel(project, key)+".plist")
}

func (m Launchd) EnableHolder(project, key string) error {
	units, err := m.HolderUnits(project)
	if err != nil {
		return err
	}
	found := false
	for _, u := range units {
		if u.Key == key {
			found = true
			continue
		}
		if err := m.removeHolderAgent(project, u.Key); err != nil {
			return err
		}
	}
	if !found {
		return errors.New("the holder job of build " + key + " is not installed")
	}
	dst := m.holderAgentPath(project, key)
	if _, err := ours(dst); err != nil {
		return err
	}
	b, err := os.ReadFile(m.holderPath(project, key))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	_, err = writeIfChanged(dst, string(b))
	return err
}

func (m Launchd) DisableHolders(project string) error {
	units, err := m.HolderUnits(project)
	if err != nil {
		return err
	}
	for _, u := range units {
		if err := m.removeHolderAgent(project, u.Key); err != nil {
			return err
		}
	}
	return nil
}

// removeHolderAgent removes the build's LaunchAgents copy; a file agentbus
// did not write is left as it is.
func (m Launchd) removeHolderAgent(project, key string) error {
	p := m.holderAgentPath(project, key)
	mine, err := ours(p)
	if errors.Is(err, errForeign) {
		return nil
	}
	if err != nil || !mine {
		return err
	}
	return os.Remove(p)
}
