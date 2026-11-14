package service

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// The holder's services (spec 2026-11-10-herdr-exit-design.md, 4.4a): one
// unit per (project, build), never a template and never restarted to
// upgrade — an upgrade writes and starts a new unit beside the old one, and
// the old build drains. Each unit runs its build's own binary copy, records
// its project, build key and raw build id, and is found by those recorded
// values, never by a name prefix. Only `service gc` removes an old build's
// unit and binary, never a running one.

// Holder is the holder's service kind.
const Holder = "holder"

// The holder's restart policy (spec 4.4a).
const (
	holderRestartSec     = 2
	holderStartInterval  = 300
	holderStartBurst     = 5
	holderThrottleSecond = 10
)

// HolderSpec is one holder unit to install: the project and the raw build id
// of the binary copy it runs (InstallHolderBinary).
type HolderSpec struct {
	Project, Raw string
	Paths        Paths
}

// HolderUnit is an installed holder unit, as its recorded values say.
type HolderUnit struct{ Project, Key, Raw string }

// HolderManager is the service manager's side of the holder's units.
type HolderManager interface {
	// InstallHolder writes the unit of s's (project, build); it refuses a key
	// already recorded with another raw id (security S7).
	InstallHolder(s HolderSpec) (bool, error)
	// StartHolder starts that build's unit; it never stops or restarts another.
	StartHolder(project, key string) error
	// HolderActive: the build's instance runs or is starting. An error means
	// the manager could not say (never "inactive").
	HolderActive(project, key string) (bool, error)
	// RemoveHolder deletes an inactive unit; it never stops one.
	RemoveHolder(project, key string) error
	// HolderUnits lists the units whose recorded project is project ("" every one).
	HolderUnits(project string) ([]HolderUnit, error)
}

// HolderUnitName is the systemd unit of project's build key.
func HolderUnitName(project, key string) string {
	return "agentbus-holder." + project + "." + key + ".service"
}

// HolderLabel is the launchd label of project's build key.
func HolderLabel(project, key string) string { return "com.agentbus.holder." + project + "." + key }

// HolderBinDir is where a build's binary copy lives, shared by every project
// on that build.
func HolderBinDir(p Paths, key string) string {
	return filepath.Join(p.DataHome, "agentbus", "holder", key)
}

// holderBin is the binary copy a unit runs.
func holderBin(p Paths, key string) string { return filepath.Join(HolderBinDir(p, key), "agentbus") }

func holderArgv(s HolderSpec) []string {
	return []string{holderBin(s.Paths, BuildKey(s.Raw)), "--project", s.Project, Holder}
}

func checkHolderSpec(s HolderSpec) error {
	if !validProject(s.Project) {
		return fmt.Errorf("invalid project %q", s.Project)
	}
	if s.Raw == "" || strings.ContainsAny(s.Raw, "\n\r") {
		return fmt.Errorf("invalid build id %q", s.Raw)
	}
	if _, err := os.Stat(holderBin(s.Paths, BuildKey(s.Raw))); err != nil {
		return fmt.Errorf("the holder binary of build %s is not installed: %w", s.Raw, err)
	}
	return nil
}

// validProject is bus.ValidName's rule (service does not import bus).
func validProject(p string) bool {
	if p == "" || len(p) > 32 || p[0] < 'a' || p[0] > 'z' {
		return false
	}
	for _, c := range p {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// InstallHolderBinary copies src as raw's binary copy (directory and binary
// 0700, owner only) and records raw beside it; a key already recorded with
// another raw id is refused (security S7). It returns the copy's path.
func InstallHolderBinary(p Paths, raw, src string) (string, error) {
	if raw == "" || strings.ContainsAny(raw, "\n\r") {
		return "", fmt.Errorf("invalid build id %q", raw)
	}
	key := BuildKey(raw)
	dir := HolderBinDir(p, key)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := realDirs(p.DataHome, "agentbus", "holder", key); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	rec := filepath.Join(dir, "build-raw")
	if b, err := os.ReadFile(rec); err == nil {
		if got := strings.TrimSpace(string(b)); got != raw {
			return "", fmt.Errorf("build key %s is recorded with raw id %q, not %q: refused", key, got, raw)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	dst := holderBin(p, key)
	if err := copyFile(src, dst, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(rec, []byte(raw+"\n"), 0o600); err != nil {
		return "", err
	}
	return dst, nil
}

// realDirs checks each level under root (agentbus, holder, the key) with
// Lstat: a symlink, or anything but a directory, is refused, so a removal
// never follows a link out of the per-build directory (reviewer-codex).
func realDirs(root string, levels ...string) error {
	path := root
	for _, l := range levels {
		path = filepath.Join(path, l)
		st, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return fmt.Errorf("%s is not a plain directory (a symlink is never followed)", path)
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// refuseOtherRaw refuses an install over a unit of the same key recorded
// with another raw id.
func refuseOtherRaw(m HolderManager, s HolderSpec) error {
	units, err := m.HolderUnits(s.Project)
	if err != nil {
		return err
	}
	key := BuildKey(s.Raw)
	for _, u := range units {
		if u.Key == key && u.Raw != s.Raw {
			return fmt.Errorf("build key %s of project %s is recorded with raw id %q, not %q: refused", key, s.Project, u.Raw, s.Raw)
		}
	}
	return nil
}

// GCHolders removes, for every build of project that is not current, its
// unit and then its binary copy — only when its instance is not active and
// no holder socket of that build answers (answers). It never stops a unit
// and never touches current; a binary another project's unit still records
// stays. An instance the manager cannot judge is kept. It returns the keys
// whose unit it removed.
func GCHolders(m HolderManager, p Paths, project, current string, answers func(key string) bool) ([]string, error) {
	units, err := m.HolderUnits(project)
	if err != nil {
		return nil, err
	}
	var removed []string
	var errs []error
	for _, u := range units {
		if u.Key == current {
			continue
		}
		active, err := m.HolderActive(project, u.Key)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if active || answers(u.Key) {
			continue
		}
		if err := m.RemoveHolder(project, u.Key); err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, u.Key)
	}
	all, err := m.HolderUnits("")
	if err != nil {
		return removed, errors.Join(append(errs, err)...)
	}
	base := filepath.Join(p.DataHome, "agentbus", "holder") + string(filepath.Separator)
	for _, key := range removed {
		if slices.ContainsFunc(all, func(u HolderUnit) bool { return u.Key == key }) {
			continue // another project's unit runs this build
		}
		dir := HolderBinDir(p, key)
		if !ValidBuildKey(key) || !strings.HasPrefix(dir, base) || filepath.Dir(dir)+string(filepath.Separator) != base {
			errs = append(errs, fmt.Errorf("build key %q is not a build key: its binary is kept", key))
			continue
		}
		if err := realDirs(p.DataHome, "agentbus", "holder", key); err != nil {
			errs = append(errs, fmt.Errorf("the binary of build %s is kept: %w", key, err))
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			errs = append(errs, err)
		}
	}
	slices.Sort(removed)
	return removed, errors.Join(errs...)
}

// --- systemd ---

func (m Systemd) holderPath(project, key string) string {
	return filepath.Join(m.dir(), HolderUnitName(project, key))
}

func systemdHolderUnit(s HolderSpec) string {
	key := BuildKey(s.Raw)
	words := make([]string, 0, 4)
	for _, a := range holderArgv(s) {
		words = append(words, systemdQuote(a))
	}
	pct := strings.NewReplacer("%", "%%")
	var b strings.Builder
	b.WriteString("# Written by `agentbus up` for one build; `agentbus service gc` removes it once that build is gone. Never restarted to upgrade.\n")
	fmt.Fprintf(&b, "[Unit]\nDescription=agentbus holder for project %s, build %s\n", s.Project, pct.Replace(s.Raw))
	fmt.Fprintf(&b, "X-Agentbus-Project=%s\nX-Agentbus-Build=%s\nX-Agentbus-Build-Raw=%s\n", s.Project, key, pct.Replace(s.Raw))
	fmt.Fprintf(&b, "StartLimitIntervalSec=%d\nStartLimitBurst=%d\n\n", holderStartInterval, holderStartBurst)
	fmt.Fprintf(&b, "[Service]\nType=simple\nExecStart=%s\n", strings.Join(words, " "))
	fmt.Fprintf(&b, "Restart=on-failure\nRestartSec=%d\nKillMode=control-group\n", holderRestartSec)
	return b.String()
}

func (m Systemd) InstallHolder(s HolderSpec) (bool, error) {
	if err := checkHolderSpec(s); err != nil {
		return false, err
	}
	if err := refuseOtherRaw(m, s); err != nil {
		return false, err
	}
	changed, err := writeIfChanged(m.holderPath(s.Project, BuildKey(s.Raw)), systemdHolderUnit(s))
	if err != nil || !changed {
		return changed, err
	}
	return true, m.ctl("daemon-reload")
}

func (m Systemd) StartHolder(project, key string) error {
	return m.ctl("start", HolderUnitName(project, key))
}

func (m Systemd) HolderActive(project, key string) (bool, error) {
	out, err := m.Run("--user", "is-active", HolderUnitName(project, key))
	switch s := strings.TrimSpace(string(out)); s {
	case "active", "activating", "reloading", "deactivating":
		return true, nil
	case "inactive", "failed", "unknown":
		return false, nil
	default:
		if err != nil {
			return false, fmt.Errorf("systemctl --user is-active %s: %s", HolderUnitName(project, key), firstLine(out, err))
		}
		return false, fmt.Errorf("systemctl --user is-active %s: unexpected %q", HolderUnitName(project, key), s)
	}
}

func (m Systemd) RemoveHolder(project, key string) error {
	name := HolderUnitName(project, key)
	if err := m.ctl("disable", name); err != nil {
		return err
	}
	if err := os.Remove(m.holderPath(project, key)); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = m.ctl("reset-failed", name)
	return m.ctl("daemon-reload")
}

func (m Systemd) HolderUnits(project string) ([]HolderUnit, error) {
	return holderUnits(m.dir(), ".service", project, readSystemdRecord)
}

// readSystemdRecord reads the X-Agentbus-* values of a unit's [Unit] section.
func readSystemdRecord(path string) (HolderUnit, bool) {
	f, err := os.Open(path)
	if err != nil {
		return HolderUnit{}, false
	}
	defer f.Close()
	var u HolderUnit
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || section != "[Unit]" {
			continue
		}
		switch k {
		case "X-Agentbus-Project":
			u.Project = v
		case "X-Agentbus-Build":
			u.Key = v
		case "X-Agentbus-Build-Raw":
			u.Raw = strings.ReplaceAll(v, "%%", "%")
		}
	}
	return u, u.Project != "" && u.Key != ""
}

// holderUnits reads every unit file of dir with suffix and keeps the ones
// whose recorded project is project ("" every one) and whose file name is
// the one its recorded values give.
func holderUnits(dir, suffix, project string, read func(string) (HolderUnit, bool)) ([]HolderUnit, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []HolderUnit
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), suffix) || !strings.Contains(e.Name(), "holder.") {
			continue
		}
		u, ok := read(filepath.Join(dir, e.Name()))
		if !ok || !ValidBuildKey(u.Key) || !validProject(u.Project) { // a record is trusted only as a key and a project: never "..", "/" (foureyes)
			continue
		}
		if project != "" && u.Project != project {
			continue
		}
		if e.Name() != HolderUnitName(u.Project, u.Key) && e.Name() != HolderLabel(u.Project, u.Key)+".plist" {
			continue // a file whose name disagrees with its record: not ours to judge
		}
		out = append(out, u)
	}
	slices.SortFunc(out, func(a, b HolderUnit) int { return strings.Compare(a.Project+"."+a.Key, b.Project+"."+b.Key) })
	return out, nil
}

// --- launchd ---

func (m Launchd) holderPath(project, key string) string {
	return filepath.Join(m.dir(), HolderLabel(project, key)+".plist")
}

func (m Launchd) holderTarget(project, key string) string {
	return "gui/" + strconv.Itoa(m.UID) + "/" + HolderLabel(project, key)
}

func launchdHolderPlist(s HolderSpec) string {
	key := BuildKey(s.Raw)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<!-- " + launchdMark + " for one holder build; `agentbus service gc` removes it once that build is gone. -->\n")
	b.WriteString(`<plist version="1.0"><dict>` + "\n")
	fmt.Fprintf(&b, "<key>Label</key><string>%s</string>\n", xmlEscape(HolderLabel(s.Project, key)))
	fmt.Fprintf(&b, "<key>XAgentbusProject</key><string>%s</string>\n", xmlEscape(s.Project))
	fmt.Fprintf(&b, "<key>XAgentbusBuild</key><string>%s</string>\n", xmlEscape(key))
	fmt.Fprintf(&b, "<key>XAgentbusBuildRaw</key><string>%s</string>\n", xmlEscape(s.Raw))
	b.WriteString("<key>ProgramArguments</key><array>")
	for _, a := range holderArgv(s) {
		fmt.Fprintf(&b, "<string>%s</string>", xmlEscape(a))
	}
	b.WriteString("</array>\n")
	b.WriteString("<key>RunAtLoad</key><true/>\n")
	b.WriteString("<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>\n")
	fmt.Fprintf(&b, "<key>ThrottleInterval</key><integer>%d</integer>\n", holderThrottleSecond)
	b.WriteString("<key>ProcessType</key><string>Interactive</string>\n")
	b.WriteString("<key>StandardOutPath</key><string>/dev/null</string>\n")
	fmt.Fprintf(&b, "<key>StandardErrorPath</key><string>%s</string>\n", xmlEscape(s.Paths.CrashFile(s.Project, Holder+"."+key)))
	b.WriteString("</dict></plist>\n")
	return b.String()
}

func (m Launchd) InstallHolder(s HolderSpec) (bool, error) {
	if err := checkHolderSpec(s); err != nil {
		return false, err
	}
	if err := refuseOtherRaw(m, s); err != nil {
		return false, err
	}
	if err := os.MkdirAll(s.Paths.StateDir(s.Project), 0o700); err != nil {
		return false, err
	}
	return writeIfChanged(m.holderPath(s.Project, BuildKey(s.Raw)), launchdHolderPlist(s))
}

// StartHolder loads the build's job (RunAtLoad starts it); a loaded job is
// left as it is — never kicked, which would restart a running holder.
func (m Launchd) StartHolder(project, key string) error {
	if ok, err := m.HolderActive(project, key); err != nil || ok {
		return err
	}
	return m.ctl("bootstrap", "gui/"+strconv.Itoa(m.UID), m.holderPath(project, key))
}

// HolderActive: the job is loaded (spec 4.4a: launchd "not loaded" is the
// inactive state gc may remove). Not loaded is launchd's own answer ("Could
// not find service"); any other failure of print is a manager that cannot
// say, an error, never "inactive" (reviewer-codex).
func (m Launchd) HolderActive(project, key string) (bool, error) {
	out, err := m.Run("print", m.holderTarget(project, key))
	switch {
	case err == nil:
		return true, nil
	case strings.Contains(string(out), "Could not find service"):
		return false, nil
	}
	return false, fmt.Errorf("launchctl print %s: %s", m.holderTarget(project, key), firstLine(out, err))
}

// RemoveHolder deletes a job that is not loaded: there is nothing to boot
// out, and a loaded one is never removed (a gc never stops a holder).
func (m Launchd) RemoveHolder(project, key string) error {
	if ok, err := m.HolderActive(project, key); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("launchd job %s is loaded: not removed", HolderLabel(project, key))
	}
	if err := os.Remove(m.holderPath(project, key)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m Launchd) HolderUnits(project string) ([]HolderUnit, error) {
	return holderUnits(m.dir(), ".plist", project, readPlistRecord)
}

// readPlistRecord reads the XAgentbus* keys agentbus wrote, one per line.
func readPlistRecord(path string) (HolderUnit, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return HolderUnit{}, false
	}
	unescape := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'", "&amp;", "&")
	var u HolderUnit
	for _, line := range strings.Split(string(b), "\n") {
		for key, dst := range map[string]*string{"XAgentbusProject": &u.Project, "XAgentbusBuild": &u.Key, "XAgentbusBuildRaw": &u.Raw} {
			pre := "<key>" + key + "</key><string>"
			if v, ok := strings.CutPrefix(line, pre); ok {
				if v, ok = strings.CutSuffix(v, "</string>"); ok {
					*dst = unescape.Replace(v)
				}
			}
		}
	}
	return u, u.Project != "" && u.Key != ""
}
