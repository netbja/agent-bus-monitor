package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Systemd manages the services as systemd --user units. The units have no
// [Install] section and are never enabled: a login or a reboot starts no
// project; `agentbus up` (or `service start`) does.
type Systemd struct {
	Run   Runner // systemctl
	Paths Paths
}

func (Systemd) Name() string { return "systemd" }

func (m Systemd) dir() string { return filepath.Join(m.Paths.ConfigHome, "systemd", "user") }

func (m Systemd) path(project, kind string) string {
	return filepath.Join(m.dir(), UnitName(project, kind))
}

// systemdQuote quotes one ExecStart word: double quotes, backslash escapes,
// and the % and $ that systemd would expand doubled.
func systemdQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$")
	return `"` + r.Replace(s) + `"`
}

func systemdUnit(s Spec) string {
	words := make([]string, 0, 8)
	for _, a := range s.argv(true) {
		words = append(words, systemdQuote(a))
	}
	pct := strings.NewReplacer("%", "%%")
	var b strings.Builder
	fmt.Fprintf(&b, "# Written by `agentbus up`; `agentbus service remove` deletes it. Never enabled at login.\n")
	fmt.Fprintf(&b, "[Unit]\nDescription=agentbus %s for project %s\n", s.Kind, s.Project)
	fmt.Fprintf(&b, "StartLimitIntervalSec=%d\nStartLimitBurst=%d\n\n", int(BudgetWindow.Seconds()), BudgetStarts)
	fmt.Fprintf(&b, "[Service]\nType=simple\nExecStart=%s\n", strings.Join(words, " "))
	if s.WorkDir != "" {
		fmt.Fprintf(&b, "WorkingDirectory=%s\n", pct.Replace(s.WorkDir))
	}
	// Exit 0 is final (stopped, project gone, another instance, invalid
	// configuration, budget spent); only a failure is restarted.
	b.WriteString("Restart=on-failure\nRestartSec=5s\nTimeoutStopSec=60\nKillMode=control-group\n")
	return b.String()
}

func (m Systemd) Install(s Spec) (bool, error) {
	changed, err := writeIfChanged(m.path(s.Project, s.Kind), systemdUnit(s))
	if err != nil || !changed {
		return changed, err
	}
	return true, m.ctl("daemon-reload")
}

func (m Systemd) ctl(args ...string) error {
	if out, err := m.Run(append([]string{"--user"}, args...)...); err != nil {
		return fmt.Errorf("systemctl --user %s: %s", strings.Join(args, " "), firstLine(out, err))
	}
	return nil
}

func (m Systemd) Start(project, kind string) error {
	_ = m.ctl("reset-failed", UnitName(project, kind)) // a spent start limit would refuse the start
	return m.ctl("start", UnitName(project, kind))
}

func (m Systemd) Stop(project, kind string) error { return m.ctl("stop", UnitName(project, kind)) }

func (m Systemd) Remove(project, kind string) error {
	p := m.path(project, kind)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return nil
	}
	_ = m.ctl("stop", UnitName(project, kind))
	if err := os.Remove(p); err != nil {
		return err
	}
	_ = m.ctl("reset-failed", UnitName(project, kind))
	return m.ctl("daemon-reload")
}

func (m Systemd) State(project, kind string) (State, error) {
	out, err := m.Run("--user", "show", UnitName(project, kind), "--no-pager",
		"--property=LoadState,ActiveState,SubState,MainPID,Result")
	if err != nil {
		return State{}, fmt.Errorf("systemctl --user show: %s", firstLine(out, err))
	}
	kv := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			kv[k] = v
		}
	}
	st := State{Installed: kv["LoadState"] == "loaded"}
	if _, err := os.Stat(m.path(project, kind)); err == nil {
		st.Installed = true
	}
	st.Status = kv["ActiveState"]
	if sub := kv["SubState"]; sub != "" && sub != st.Status {
		st.Status += " (" + sub + ")"
	}
	st.Active = kv["ActiveState"] == "active" || kv["ActiveState"] == "activating" || kv["ActiveState"] == "reloading"
	st.PID, _ = strconv.Atoi(kv["MainPID"])
	return st, nil
}

func (m Systemd) List() ([]Unit, error) {
	entries, err := os.ReadDir(m.dir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Unit
	for _, e := range entries {
		if p, k, ok := parseUnitName(e.Name()); ok {
			out = append(out, Unit{Project: p, Kind: k})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Project+out[i].Kind < out[j].Project+out[j].Kind })
	return out, nil
}

func firstLine(out []byte, err error) string {
	s := strings.TrimSpace(string(out))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" && err != nil {
		s = err.Error()
	}
	return s
}

func itoa(n int) string { return strconv.Itoa(n) }
