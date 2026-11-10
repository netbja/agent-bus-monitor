package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/backup"
	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/netbja/agent-bus-monitor/projfiles"
)

// The archive a backup run made is the "backup: <path>" line of this run,
// after its own header: never an earlier run's, never a note, never a
// relative path (projects-admin §5.5, Q7).
func TestBackupArchiveInLog(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "suspend.log")
	write := func(s string) {
		if err := os.WriteFile(log, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	one, two := filepath.Join(dir, "agentbus-backup-box-1.tar.zst"), filepath.Join(dir, "agentbus-backup-box-2.tar.zst")
	if err := os.Mkdir(filepath.Join(dir, "d.tar.zst"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{one, two} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// after_command and the retention print after the path (foureyes).
	write("--- 2026-11-10T09:00:00Z agentbus backup --project old\nbackup: " + one + "\n" +
		"--- 2026-11-10T09:10:00Z agentbus backup --project old\nbackup: " + two + "\n  old: 12 keys\n  note: Redis: each project is read at one instant\n" +
		"  backup.after_command: ok\n  removed 2 (backup.keep = 5)\n")
	if got := backupArchiveInLog(log); got != two {
		t.Errorf("got %q", got)
	}
	// This run named no archive: the earlier run's is never taken.
	write("--- x agentbus backup\nbackup: " + one + "\n--- y agentbus backup\n  note: nothing\n")
	if got := backupArchiveInLog(log); got != "" {
		t.Errorf("an earlier run's archive: %q", got)
	}
	for _, s := range []string{
		"--- x agentbus backup\nbackup: /b/a.tar.zst\n--- y agentbus backup\nbackup: warning: backup.dir is the default\n",
		"--- y agentbus backup\nbackup: b/agentbus-backup-box-2.tar.zst\n",
		"--- y agentbus backup\nbackup: /b/notes.txt\n",
		"--- y agentbus backup\nbackup: " + filepath.Join(dir, "gone.tar.zst") + "\n", // not an existing file
		"--- y agentbus backup\nbackup: " + filepath.Join(dir, "d.tar.zst") + "\n", // a directory, not a file
		"",
	} {
		write(s)
		if got := backupArchiveInLog(log); got != "" {
			t.Errorf("%q: got %q, want none", s, got)
		}
	}
}

// filesWorld is a delete world whose project old has a template, a
// worktree and archives on disk, and the two options on.
type filesWorld struct {
	*deleteWorld
	tmpl, w, wt, dir, old, fresh string
}

func newFilesWorld(t *testing.T) *filesWorld {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := &filesWorld{deleteWorld: newDeleteWorld(t), tmpl: filepath.Join(root, "projects", "old.toml"),
		w: filepath.Join(root, "w"), dir: filepath.Join(root, "backups")}
	put(f.tmpl, "working_dir = \""+f.w+"\"\n")
	put(filepath.Join(f.w, ".git", "HEAD"), "ref: refs/heads/main\n")
	f.wt = filepath.Join(f.w, ".worktrees", "a")
	entry := filepath.Join(f.w, ".git", "worktrees", "a")
	put(filepath.Join(entry, "gitdir"), filepath.Join(f.wt, ".git")+"\n")
	put(filepath.Join(entry, "HEAD"), "ref: refs/heads/fix/a\n")
	put(filepath.Join(f.wt, ".git"), "gitdir: "+entry+"\n")
	t0 := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	f.old = f.archive(t, t0)
	src := *f.m.cfg.admin
	src.files = func(p string) (projfiles.Options, error) {
		return projfiles.Options{Project: p, Template: f.tmpl, WorkingDir: f.w, BackupDir: f.dir, Host: "box"}, nil
	}
	f.m.cfg.admin = &src
	f.m.cfg.settings.DeleteFiles, f.m.cfg.settings.DeleteBackups = true, true
	return f
}

func (f *filesWorld) archive(t *testing.T, at time.Time) string {
	t.Helper()
	var b bytes.Buffer
	aw, err := backup.NewWriter(&b, backup.Coverage{Format: backup.Format, CreatedAt: at, Host: "box", Projects: []string{"old"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := aw.Close(backup.Manifest{}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(f.dir, backup.ArchiveName("box", at))
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// backedUp ends the panel's backup run with archive as the path its log names.
func (f *filesWorld) backedUp(t *testing.T, archive string) {
	t.Helper()
	r := f.m.ui.sleepRun
	if r == nil {
		t.Fatal("no run")
	}
	next, cmd := f.m.Update(sleepDoneMsg{id: r.id, last: "  note: Redis: each project is read at one instant", archive: archive})
	f.m = next.(Model)
	f.feedAdmin(t, cmd)
}

func gone(p string) bool { _, err := os.Lstat(p); return os.IsNotExist(err) }

// §5.5 with both options on: the panel lists what goes, the archive b made
// is shown kept, and after a clean delete the template, the worktree and the
// older archive go while the fresh one stays (the person, Q7).
func TestProjectsDeleteAlsoRemovesFiles(t *testing.T) {
	f := newFilesWorld(t)
	f.selectRow(t, "old")
	f.press(t, 'd')
	panel := linesPlain(f.m.deletePanelLines(400))
	for _, want := range []string{"also deleted (settings)", "template " + f.tmpl, "worktrees: .worktrees/a (fix/a)", "backups: 1 archive of old alone"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("panel lacks %q:\n%s", want, panel)
		}
	}
	f.press(t, 'b')
	f.fresh = f.archive(t, time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC))
	f.backedUp(t, f.fresh)
	if d := f.m.ui.del; d == nil || d.step != delName || d.backup != f.fresh {
		t.Fatalf("after the backup: %+v", d)
	}
	if lines := linesPlain(f.m.deletePanelLines(400)); !strings.Contains(lines, "kept: "+f.fresh+" (made now)") {
		t.Fatalf("panel lacks the kept archive:\n%s", lines)
	}
	f.typeName(t, "old")
	f.press(t, tea.Key{Code: tea.KeyEnter})
	if !gone(f.tmpl) || !gone(f.wt) || !gone(f.old) || gone(f.fresh) || gone(f.w) {
		t.Fatalf("template gone %v, worktree gone %v, old archive gone %v, fresh kept %v, checkout kept %v",
			gone(f.tmpl), gone(f.wt), gone(f.old), !gone(f.fresh), !gone(f.w))
	}
	if !noticeHas(f.m, "also removed: the template, 1 worktree, 1 archive") {
		t.Errorf("notices %v", f.m.notice.queue)
	}
}

// With n (no backup), no archive is exempt.
func TestProjectsDeleteNoBackupKeepsNone(t *testing.T) {
	f := newFilesWorld(t)
	f.selectRow(t, "old")
	f.press(t, 'd')
	f.press(t, 'n')
	f.typeName(t, "old")
	f.press(t, tea.Key{Code: tea.KeyEnter})
	if !gone(f.old) {
		t.Error("the archive stayed")
	}
}

// A backup whose archive path cannot be read from its log deletes no
// archive at all: the fresh one might be among them (fail closed).
func TestProjectsDeleteUnknownArchiveKeepsTheBackups(t *testing.T) {
	f := newFilesWorld(t)
	f.selectRow(t, "old")
	f.press(t, 'd')
	f.press(t, 'b')
	f.backedUp(t, "")
	if lines := linesPlain(f.m.deletePanelLines(400)); !strings.Contains(lines, "backups: kept, the new archive's path is not in the log") {
		t.Fatalf("panel:\n%s", lines)
	}
	f.typeName(t, "old")
	f.press(t, tea.Key{Code: tea.KeyEnter})
	if gone(f.old) || !gone(f.tmpl) {
		t.Errorf("archive gone %v (want kept), template gone %v", gone(f.old), gone(f.tmpl))
	}
}

// Keys that came back, or a failed delete: no file is touched.
func TestProjectsDeleteFilesOnlyAfterACleanDelete(t *testing.T) {
	for _, res := range []deleteResult{{keys: 3, left: []string{"old:status"}}, {err: os.ErrPermission}} {
		f := newFilesWorld(t)
		f.result = res
		f.selectRow(t, "old")
		f.press(t, 'd')
		f.press(t, 'n')
		f.typeName(t, "old")
		f.press(t, tea.Key{Code: tea.KeyEnter})
		if gone(f.tmpl) || gone(f.wt) || gone(f.old) || !noticeHas(f.m, "no file removed") {
			t.Errorf("%+v: a file went (template %v, worktree %v, archive %v); notices %v", res, gone(f.tmpl), gone(f.wt), gone(f.old), f.m.notice.queue)
		}
	}
}

// Both off: the panel says the files stay, and nothing is removed.
func TestProjectsDeleteBothOffKeepsFiles(t *testing.T) {
	f := newFilesWorld(t)
	f.m.cfg.settings.DeleteFiles, f.m.cfg.settings.DeleteBackups = false, false
	f.selectRow(t, "old")
	f.press(t, 'd')
	if panel := linesPlain(f.m.deletePanelLines(400)); !strings.Contains(panel, "The template and the worktrees stay.") || strings.Contains(panel, "also deleted") {
		t.Fatalf("panel:\n%s", panel)
	}
	f.press(t, 'n')
	f.typeName(t, "old")
	f.press(t, tea.Key{Code: tea.KeyEnter})
	if gone(f.tmpl) || gone(f.wt) || gone(f.old) {
		t.Error("a file went with both options off")
	}
}

// busmon --delete honours the two options (§5.5): it removes the files only
// after a clean delete, with no backup made, so no archive is exempt; a
// declined prompt removes nothing.
func TestDeleteCommandAlsoRemovesFiles(t *testing.T) {
	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	project := "df" + strconv.FormatInt(time.Now().UnixNano(), 36)
	f := newFilesWorld(t)
	files := func() (projfiles.Options, error) {
		return projfiles.Options{Project: project, Template: filepath.Join(filepath.Dir(f.tmpl), project+".toml"),
			WorkingDir: f.w, BackupDir: f.dir, Host: "box", Files: true, Backups: true}, nil
	}
	tmpl := filepath.Join(filepath.Dir(f.tmpl), project+".toml")
	if err := os.WriteFile(tmpl, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := bus.Open(c, project)
	b.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}})
	t.Cleanup(func() { c.Del(ctx, bus.ServicesKey(project)) })

	if err := deleteProject("", project, false, strings.NewReader("n\n"), time.Now(), nil, files); err != nil {
		t.Fatal(err)
	}
	if gone(tmpl) || gone(f.wt) {
		t.Fatal("a declined delete removed files")
	}
	if err := deleteProject("", project, true, strings.NewReader(""), time.Now(), nil, files); err != nil {
		t.Fatal(err)
	}
	if !gone(tmpl) || !gone(f.wt) || gone(f.w) {
		t.Errorf("template gone %v, worktree gone %v, checkout kept %v", gone(tmpl), gone(f.wt), !gone(f.w))
	}
}

// What --delete prints before asking: the panel's own lines.
func TestDeleteCommandFilesText(t *testing.T) {
	p := projfiles.Plan{Template: "/p/demo.toml", Archives: []string{"a"}}
	got := filesPlanText("demo", p, nil)
	if !strings.Contains(got, "also deleted (settings):\n  template /p/demo.toml\n  backups: 1 archive of demo alone") {
		t.Errorf("got %q", got)
	}
	if got := filesPlanText("demo", projfiles.Plan{}, errors.New("boom")); !strings.Contains(got, "no file will be removed") {
		t.Errorf("got %q", got)
	}
}

// Where the files are cannot be read: the panel and --delete say no file
// will be removed, and none is, even after a clean delete.
func TestDeleteUnreadableFilesRemoveNone(t *testing.T) {
	f := newFilesWorld(t)
	src := *f.m.cfg.admin
	inner := src.files
	src.files = func(p string) (projfiles.Options, error) {
		o, _ := inner(p)
		return o, errors.New("the templates: permission denied")
	}
	f.m.cfg.admin = &src
	f.selectRow(t, "old")
	f.press(t, 'd')
	if panel := linesPlain(f.m.deletePanelLines(400)); !strings.Contains(panel, "no file will be removed") {
		t.Fatalf("panel:\n%s", panel)
	}
	f.press(t, 'n')
	f.typeName(t, "old")
	f.press(t, tea.Key{Code: tea.KeyEnter})
	if gone(f.tmpl) || gone(f.wt) || gone(f.old) {
		t.Error("the panel removed files it said it would not")
	}

	testredis.Configure(t)
	c, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	project := "du" + strconv.FormatInt(time.Now().UnixNano(), 36)
	tmpl := filepath.Join(filepath.Dir(f.tmpl), project+".toml")
	if err := os.WriteFile(tmpl, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := bus.Open(c, project)
	b.MarkServicesInstalled(ctx, bus.ServicesInstall{Kinds: []string{"dispatch"}})
	t.Cleanup(func() { c.Del(ctx, bus.ServicesKey(project)) })
	files := func() (projfiles.Options, error) {
		return projfiles.Options{Project: project, Template: tmpl, WorkingDir: f.w, Files: true}, errors.New("permission denied")
	}
	if err := deleteProject("", project, true, strings.NewReader(""), time.Now(), nil, files); err != nil {
		t.Fatal(err)
	}
	if gone(tmpl) || gone(f.wt) {
		t.Error("--delete removed files it said it would not")
	}
}
