package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/backup"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/config"
)

// backupWorld is a config home whose backup.dir holds two archives of this
// host (one with secrets, one of another host) and a state folder with
// backup-last.json.
type backupWorld struct {
	getenv        func(string) string
	dir, state    string
	newest, older string // archive paths
	runs          [][]string
	logs          []string
	host          string
}

func newBackupWorld(t *testing.T, cfgExtra string) *backupWorld {
	t.Helper()
	w := &backupWorld{dir: filepath.Join(t.TempDir(), "my backups"), state: t.TempDir()}
	w.host, _ = os.Hostname()
	w.getenv = settingsEnv(t, "[backup]\ndir = \""+w.dir+"\"\n"+cfgExtra, "",
		map[string]string{"XDG_STATE_HOME": w.state, "AGENT_BUS_NO_UPDATE_CHECK": "1"})
	mk := func(host string, at time.Time, projects []string, secrets []byte) string {
		res, err := backup.Run(context.Background(), w.dir, backup.Plan{Projects: projects, Host: host, Now: at, Version: "dev",
			Dump: func(context.Context, string) (bus.ProjectDump, error) { return bus.ProjectDump{}, nil }, Secrets: secrets})
		if err != nil {
			t.Fatal(err)
		}
		return res.Path
	}
	w.older = mk(w.host, time.Date(2026, 11, 2, 3, 0, 0, 0, time.UTC), []string{"alpha"}, nil)
	w.newest = mk(w.host, time.Date(2026, 11, 3, 3, 0, 0, 0, time.UTC), []string{"alpha", "beta"}, []byte("age-ciphertext"))
	mk("elsewhere-host", time.Date(2026, 11, 4, 3, 0, 0, 0, time.UTC), []string{"gamma"}, nil)
	last := filepath.Join(w.state, "agentbus", "backup-last.json")
	if err := backup.RecordScheduled(last, backup.ScheduledRun{At: time.Date(2026, 11, 3, 3, 0, 0, 0, time.UTC),
		Error: "broker: password=hunter2 refused", Projects: []string{"alpha"}}); err != nil {
		t.Fatal(err)
	}
	if err := backup.RecordAfter(last, []string{"alpha"}, backup.AfterEntry{At: time.Date(2026, 11, 3, 3, 1, 0, 0, time.UTC), Archive: "x", Outcome: "exit 2"}); err != nil {
		t.Fatal(err)
	}
	return w
}

// The reader: this host's archives only, newest first, each with its size,
// its projects and whether it holds secrets; the last runs from the record,
// an error's secret redacted.
func TestReadBackupFacts(t *testing.T) {
	w := newBackupWorld(t, "")
	f := readBackupFacts(w.getenv, w.host)
	if f.Err != "" || f.Dir != w.dir || f.Defaulted {
		t.Fatalf("facts %+v", f)
	}
	if len(f.Archives) != 2 || f.Archives[0].Path != w.newest || f.Archives[1].Path != w.older {
		t.Fatalf("archives %+v", f.Archives)
	}
	a := f.Archives[0]
	if strings.Join(a.Projects, ",") != "alpha,beta" || !a.Secrets || a.Size <= 0 || f.Archives[1].Secrets {
		t.Fatalf("newest %+v", a)
	}
	last := strings.Join(f.Last, "\n")
	if !strings.Contains(last, "failed") || strings.Contains(last, "hunter2") || !strings.Contains(last, "exit 2 (alpha)") {
		t.Fatalf("last %q", last)
	}
	if f.Log != filepath.Join(w.state, "agentbus", "backup.log") {
		t.Fatalf("log %q", f.Log)
	}
	if got := strings.Join(backupNowArgs(f), " "); got != "backup" {
		t.Fatalf("args %q", got)
	}
	recipient := newBackupWorld(t, "age_recipient = \"age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3290gq\"\n")
	defer func(l func(string) (string, error)) { lookAge = l }(lookAge)
	lookAge = func(string) (string, error) { return "/usr/bin/age", nil }
	f = readBackupFacts(recipient.getenv, w.host)
	if got := strings.Join(backupNowArgs(f), " "); got != "backup --secrets" {
		t.Fatalf("with a recipient: args %q (err %q)", got, f.Err)
	}
	// foureyes: a recipient but no age, the run leaves the secrets out, as
	// the schedule does, and the line says why.
	lookAge = func(string) (string, error) { return "", exec.ErrNotFound }
	f = readBackupFacts(recipient.getenv, w.host)
	if got := strings.Join(backupNowArgs(f), " "); got != "backup" || !f.NoAge {
		t.Fatalf("no age: args %q, facts %+v", got, f)
	}
	lines, _ := backupItemLines(&settingsPage{}, &f, 0, -1, 200, "")
	if txt := tokLinesText(lines); !strings.Contains(txt, "age is not on PATH") {
		t.Fatalf("the line does not say age is missing:\n%s", txt)
	}
}

// With no HOME and no XDG folder nothing is read: the folder is unknown.
func TestReadBackupFactsNoHome(t *testing.T) {
	f := readBackupFacts(func(string) string { return "" }, "h")
	if f.Err == "" || len(f.Archives) != 0 || f.Log != "" {
		t.Fatalf("%+v", f)
	}
}

func backupShell(t *testing.T, w *backupWorld) Model {
	t.Helper()
	var themed string
	m := settingsShell(t, w.getenv, &themed)
	m.cfg.sleepRunner = func(args []string, log string) (<-chan error, error) {
		w.runs = append(w.runs, args)
		w.logs = append(w.logs, log)
		return make(chan error), nil
	}
	m = showGroup(t, press(t, m, 'C'), config.GroupBackup)
	m.rd.top().set.keys = true
	return m
}

// selectBackupItem selects the Backup group's line i after its keys (0 =
// Back up now).
func selectBackupItem(t *testing.T, m Model, i int) Model {
	t.Helper()
	p := m.rd.top()
	p.sel, p.dirty = len(p.set.rows())+i, true
	m.refresh()
	return m
}

// The page: under the keys the folder, Back up now and one row per archive
// of this host with its date, projects and secrets; ↓ walks from the keys
// onto them.
func TestSettingsBackupArchivesShown(t *testing.T) {
	w := newBackupWorld(t, "")
	m := backupShell(t, w)
	body := tokLinesText(m.rd.top().body)
	for _, s := range []string{"Archives", w.dir, "Back up now", "alpha, beta", "secrets", "no secrets", "last scheduled run", "exit 2"} {
		if !strings.Contains(body, s) {
			t.Errorf("page lacks %q:\n%s", s, body)
		}
	}
	if strings.Contains(body, "gamma") || strings.Contains(body, "hunter2") {
		t.Fatalf("another host's archive or a secret shown:\n%s", body)
	}
	for range m.rd.top().set.rows() {
		m = press(t, m, tea.Key{Code: tea.KeyDown})
	}
	if p := m.rd.top(); p.sel != len(p.set.rows()) {
		t.Fatalf("↓ stops at %d, want Back up now (%d)", p.sel, len(p.set.rows()))
	}
	m = press(t, m, tea.Key{Code: tea.KeyDown})
	m = press(t, m, tea.Key{Code: tea.KeyDown})
	m = press(t, m, tea.Key{Code: tea.KeyDown})
	if p := m.rd.top(); p.sel != len(p.set.rows())+2 {
		t.Fatalf("↓ past the last archive: sel %d", p.sel)
	}
}

// Back up now asks y, then runs agentbus backup as Z runs suspend, its log
// backup.log; any other key runs nothing; a second one while it runs says so.
func TestSettingsBackupNow(t *testing.T) {
	w := newBackupWorld(t, "")
	m := selectBackupItem(t, backupShell(t, w), 0)
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if !m.rd.top().set.backupAsk || !strings.Contains(tokLinesText(m.rd.top().body), "Back up every project of this host now?") {
		t.Fatal("no question")
	}
	m = press(t, m, 'n')
	if m.rd.top().set.backupAsk || m.ui.sleepRun != nil {
		t.Fatal("n ran something")
	}
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	m = next.(Model)
	if cmd == nil || m.ui.sleepRun == nil || m.ui.sleepRun.verb != "backup" {
		t.Fatal("y started no run")
	}
	started := cmd()
	if len(w.runs) != 1 || strings.Join(w.runs[0], " ") != "backup" || w.logs[0] != filepath.Join(w.state, "agentbus", "backup.log") {
		t.Fatalf("runs %q logs %q", w.runs, w.logs)
	}
	next, _ = m.Update(started)
	m = next.(Model)
	m = press(t, m, tea.Key{Code: tea.KeyEnter})
	if !strings.Contains(m.ui.flash, "backup already running") || len(w.runs) != 1 {
		t.Fatalf("second run: flash %q, runs %d", m.ui.flash, len(w.runs))
	}
	if body := tokLinesText(m.rd.top().body); !strings.Contains(body, "running") {
		t.Fatalf("the line does not say it runs:\n%s", body)
	}
	// Its end is the notice and reads the page again (the new archive).
	next, cmd = m.Update(sleepDoneMsg{id: m.ui.sleepRun.id})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("the end reads nothing again")
	}
	if _, ok := cmd().(settingsMsg); !ok {
		t.Fatal("the end's command is not the page's read")
	}
}

// Enter on an archive opens its coverage and the restore dry runs to copy,
// the path quoted for a shell; busmon restores nothing.
func TestSettingsBackupArchivePage(t *testing.T) {
	w := newBackupWorld(t, "")
	m := press(t, selectBackupItem(t, backupShell(t, w), 1), tea.Key{Code: tea.KeyEnter})
	p := m.rd.top()
	if p.kind != ovMessage {
		t.Fatalf("Enter opened %v", p.kind)
	}
	body := tokLinesText(p.body)
	q := "'" + w.newest + "'"
	for _, s := range []string{"alpha, beta", "included, encrypted", "agentbus restore " + q + " --dry-run", "agentbus restore " + q + " --project beta --dry-run"} {
		if !strings.Contains(body, s) {
			t.Errorf("page lacks %q:\n%s", s, body)
		}
	}
	// A restore refuses a live project whatever --force: the page names
	// every step that takes one offline, not a suspend alone (reviewer-codex).
	for _, s := range []string{"even with --force", "agentbus down alpha", "agentbus down beta", "close its herdr workspace",
		"its agents exit with it", "a suspend is not enough", "agentbus up <project> --resume"} {
		if !strings.Contains(body, s) {
			t.Errorf("restore guidance lacks %q:\n%s", s, body)
		}
	}
	if strings.Contains(body, "agentbus suspend it first") {
		t.Errorf("the page still says a suspend is enough:\n%s", body)
	}
	if !strings.HasPrefix(p.plain, "agentbus restore "+q+" --dry-run\n") || strings.Count(p.plain, "\n") != 2 {
		t.Fatalf("y copies %q", p.plain)
	}
	if len(w.runs) != 0 {
		t.Fatal("opening an archive ran something")
	}
}

func TestShellWord(t *testing.T) {
	for in, want := range map[string]string{"/a/b.tar.zst": "/a/b.tar.zst", "/my dir/x": "'/my dir/x'", "it's": `'it'\''s'`, "$(x)": "'$(x)'"} {
		if got := shellWord(in); got != want {
			t.Errorf("shellWord(%q) = %q, want %q", in, got, want)
		}
	}
}
