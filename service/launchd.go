package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Launchd manages the services as launchd jobs in the user's GUI domain. The
// plists live in ~/.local/share/agentbus/launchd, not ~/Library/LaunchAgents,
// so launchd never loads them at login: `agentbus up` bootstraps them.
type Launchd struct {
	Run   Runner // launchctl
	Paths Paths
	UID   int
}

func (Launchd) Name() string { return "launchd" }

func (m Launchd) dir() string { return filepath.Join(m.Paths.DataHome, "agentbus", "launchd") }

func (m Launchd) path(project, kind string) string {
	return filepath.Join(m.dir(), Label(project, kind)+".plist")
}

func (m Launchd) target(project, kind string) string {
	return "gui/" + strconv.Itoa(m.UID) + "/" + Label(project, kind)
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}

// launchdPlist: restart on a non-zero exit only (KeepAlive SuccessfulExit
// false), at most every 10 s; the service's own budget bounds the rest. Its
// log is its own rotating file; launchd's stdout/stderr catch only a crash.
func launchdPlist(s Spec) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<!-- Written by `agentbus up`; `agentbus service remove` deletes it. Not loaded at login. -->\n")
	b.WriteString(`<plist version="1.0"><dict>` + "\n")
	fmt.Fprintf(&b, "<key>Label</key><string>%s</string>\n", xmlEscape(Label(s.Project, s.Kind)))
	b.WriteString("<key>ProgramArguments</key><array>")
	for _, a := range s.argv(false) {
		fmt.Fprintf(&b, "<string>%s</string>", xmlEscape(a))
	}
	b.WriteString("</array>\n")
	if s.WorkDir != "" {
		fmt.Fprintf(&b, "<key>WorkingDirectory</key><string>%s</string>\n", xmlEscape(s.WorkDir))
	}
	crash := filepath.Join(s.Paths.StateDir(s.Project), s.Kind+".crash.log")
	b.WriteString("<key>RunAtLoad</key><true/>\n")
	b.WriteString("<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>\n")
	b.WriteString("<key>ThrottleInterval</key><integer>10</integer>\n")
	b.WriteString("<key>ExitTimeOut</key><integer>60</integer>\n")
	b.WriteString("<key>ProcessType</key><string>Background</string>\n")
	fmt.Fprintf(&b, "<key>StandardOutPath</key><string>%s</string>\n", xmlEscape(crash))
	fmt.Fprintf(&b, "<key>StandardErrorPath</key><string>%s</string>\n", xmlEscape(crash))
	b.WriteString("</dict></plist>\n")
	return b.String()
}

func (m Launchd) Install(s Spec) (bool, error) {
	if err := os.MkdirAll(s.Paths.StateDir(s.Project), 0o700); err != nil {
		return false, err
	}
	return writeIfChanged(m.path(s.Project, s.Kind), launchdPlist(s))
}

func (m Launchd) loaded(project, kind string) bool {
	_, err := m.Run("print", m.target(project, kind))
	return err == nil
}

func (m Launchd) ctl(args ...string) error {
	if out, err := m.Run(args...); err != nil {
		return fmt.Errorf("launchctl %s: %s", strings.Join(args, " "), firstLine(out, err))
	}
	return nil
}

// Start loads the job (RunAtLoad starts it) or, when it is loaded, kicks it.
// A changed plist is picked up by booting the old job out first.
func (m Launchd) Start(project, kind string) error {
	if m.loaded(project, kind) {
		return m.ctl("kickstart", m.target(project, kind))
	}
	return m.ctl("bootstrap", "gui/"+strconv.Itoa(m.UID), m.path(project, kind))
}

func (m Launchd) Stop(project, kind string) error {
	if !m.loaded(project, kind) {
		return nil
	}
	return m.ctl("bootout", m.target(project, kind))
}

func (m Launchd) Remove(project, kind string) error {
	if err := m.Stop(project, kind); err != nil {
		return err
	}
	if err := os.Remove(m.path(project, kind)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m Launchd) State(project, kind string) (State, error) {
	st := State{Status: "not loaded"}
	if _, err := os.Stat(m.path(project, kind)); err == nil {
		st.Installed = true
	}
	out, err := m.Run("print", m.target(project, kind))
	if err != nil {
		return st, nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), " = ")
		if !ok {
			continue
		}
		switch k {
		case "state":
			st.Status = v
			st.Active = v == "running"
		case "pid":
			st.PID, _ = strconv.Atoi(v)
		}
	}
	return st, nil
}

func (m Launchd) List() ([]Unit, error) {
	entries, err := os.ReadDir(m.dir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Unit
	for _, e := range entries {
		label, ok := strings.CutSuffix(e.Name(), ".plist")
		if !ok {
			continue
		}
		if p, k, ok := parseLabel(label); ok {
			out = append(out, Unit{Project: p, Kind: k})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Project+out[i].Kind < out[j].Project+out[j].Kind })
	return out, nil
}
