package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The updater service (quiet-update-auto §4.1, step 4e): one per host, for a
// managed build, no project. It runs `agentbus service run updater`, whose
// loop starts each automatic pass as a child; stopping the loop never kills a
// pass in flight (systemd KillMode=process, launchd AbandonProcessGroup).
// Its names hold no project and no "holder.", so it is never listed as a
// project's service (parseUnitName, parseLabel) nor as a holder.

// Updater is the updater's service kind.
const Updater = "updater"

// UpdaterUnitName is the updater's systemd unit; UpdaterLabel its launchd job.
const (
	UpdaterUnitName = "agentbus-updater.service"
	UpdaterLabel    = "dev.agentbus.updater"
)

// UpdaterEnvFile is the updater's environment file, written by agentbus up
// from its shell (CollectEnv: PATH, the broker, the Go variables a build
// needs). Not under services/: a project may be named "updater".
func (p Paths) UpdaterEnvFile() string { return filepath.Join(p.ConfigHome, "agentbus", "updater.env") }

// UpdaterLog is where launchd puts the updater's output (systemd: the journal).
func (p Paths) UpdaterLog() string { return filepath.Join(p.StateHome, "agentbus", "updater.log") }

// UpdaterSpec is the updater's unit: the agentbus installed now (a pass runs
// the newest logic) and its working directory.
type UpdaterSpec struct {
	Exe, WorkDir string
	Paths        Paths
}

func (s UpdaterSpec) argv() []string {
	return []string{s.Exe, "service", "run", Updater, "--env", s.Paths.UpdaterEnvFile()}
}

// UpdaterManager is the service manager's side of the updater's unit.
type UpdaterManager interface {
	// InstallUpdater writes or updates the unit; it reports whether anything changed.
	InstallUpdater(s UpdaterSpec) (bool, error)
	StartUpdater() error
	// StopUpdater stops the loop; a pass in flight runs on.
	StopUpdater() error
	// RemoveUpdater stops the loop and deletes its unit; a missing unit is no error.
	RemoveUpdater() error
	UpdaterState() (State, error)
}

var (
	_ UpdaterManager = Systemd{}
	_ UpdaterManager = Launchd{}
)

// --- systemd ---

func (m Systemd) updaterPath() string { return filepath.Join(m.dir(), UpdaterUnitName) }

func systemdUpdaterUnit(s UpdaterSpec) string {
	words := make([]string, 0, 6)
	for _, a := range s.argv() {
		words = append(words, systemdQuote(a))
	}
	pct := strings.NewReplacer("%", "%%")
	var b strings.Builder
	b.WriteString("# Written by `agentbus up` (the automatic update's loop, one per host); `setup uninstall` removes it. Never restarted by an update.\n")
	b.WriteString("[Unit]\nDescription=agentbus updater (the automatic update's loop)\n\n")
	fmt.Fprintf(&b, "[Service]\nType=simple\nExecStart=%s\n", strings.Join(words, " "))
	if s.WorkDir != "" {
		fmt.Fprintf(&b, "WorkingDirectory=%s\n", pct.Replace(s.WorkDir))
	}
	// Exit 0 is final (a developer build, a stop); a failure restarts. Only
	// the loop is stopped: a pass in flight, its child, runs on.
	b.WriteString("Restart=on-failure\nRestartSec=30s\nTimeoutStopSec=30\nKillMode=process\n")
	return b.String()
}

func (m Systemd) InstallUpdater(s UpdaterSpec) (bool, error) {
	changed, err := writeIfChanged(m.updaterPath(), systemdUpdaterUnit(s))
	if err != nil || !changed {
		return changed, err
	}
	return true, m.ctl("daemon-reload")
}

func (m Systemd) StartUpdater() error {
	_ = m.ctl("reset-failed", UpdaterUnitName)
	return m.ctl("start", UpdaterUnitName)
}

func (m Systemd) StopUpdater() error { return m.ctl("stop", UpdaterUnitName) }

func (m Systemd) RemoveUpdater() error {
	if _, err := os.Stat(m.updaterPath()); os.IsNotExist(err) {
		return nil
	}
	if err := m.StopUpdater(); err != nil {
		return err
	}
	if err := os.Remove(m.updaterPath()); err != nil {
		return err
	}
	_ = m.ctl("reset-failed", UpdaterUnitName)
	return m.ctl("daemon-reload")
}

func (m Systemd) UpdaterState() (State, error) { return m.showState(UpdaterUnitName, m.updaterPath()) }

// --- launchd ---

func (m Launchd) updaterPath() string { return filepath.Join(m.dir(), UpdaterLabel+".plist") }

func launchdUpdaterPlist(s UpdaterSpec) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<!-- " + launchdMark + " (the automatic update's loop, one per host); `setup uninstall` removes it. -->\n")
	b.WriteString(`<plist version="1.0"><dict>` + "\n")
	fmt.Fprintf(&b, "<key>Label</key><string>%s</string>\n", UpdaterLabel)
	b.WriteString("<key>ProgramArguments</key><array>")
	for _, a := range s.argv() {
		fmt.Fprintf(&b, "<string>%s</string>", xmlEscape(a))
	}
	b.WriteString("</array>\n")
	if s.WorkDir != "" {
		fmt.Fprintf(&b, "<key>WorkingDirectory</key><string>%s</string>\n", xmlEscape(s.WorkDir))
	}
	b.WriteString("<key>RunAtLoad</key><true/>\n")
	b.WriteString("<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>\n")
	b.WriteString("<key>ThrottleInterval</key><integer>30</integer>\n")
	// An unload signals the loop alone: a pass in flight runs on.
	b.WriteString("<key>AbandonProcessGroup</key><true/>\n")
	b.WriteString("<key>ProcessType</key><string>Background</string>\n")
	b.WriteString("<key>StandardOutPath</key><string>/dev/null</string>\n")
	fmt.Fprintf(&b, "<key>StandardErrorPath</key><string>%s</string>\n", xmlEscape(s.Paths.UpdaterLog()))
	b.WriteString("</dict></plist>\n")
	return b.String()
}

func (m Launchd) InstallUpdater(s UpdaterSpec) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(s.Paths.UpdaterLog()), 0o700); err != nil {
		return false, err
	}
	return writeIfChanged(m.updaterPath(), launchdUpdaterPlist(s))
}

func (m Launchd) StartUpdater() error { return m.startJob(UpdaterLabel, m.updaterPath()) }

func (m Launchd) StopUpdater() error { return m.stopJob(UpdaterLabel) }

func (m Launchd) RemoveUpdater() error {
	if err := m.StopUpdater(); err != nil {
		return err
	}
	if err := os.Remove(m.updaterPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m Launchd) UpdaterState() (State, error) { return m.jobState(UpdaterLabel, m.updaterPath()) }
