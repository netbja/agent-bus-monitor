package projfiles

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/backup"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// repo is a checkout W with a .git directory, as git leaves it.
func repo(t *testing.T) (root, w string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w = filepath.Join(root, "w")
	write(t, filepath.Join(w, ".git", "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(w, "cmd", "main.go"), "package main\n")
	return root, w
}

// worktree is what git worktree add writes: the entry's gitdir names D/.git,
// D/.git names the entry back.
func worktree(t *testing.T, w, name, dir, branch string) {
	t.Helper()
	entry := filepath.Join(w, ".git", "worktrees", name)
	write(t, filepath.Join(entry, "gitdir"), filepath.Join(dir, ".git")+"\n")
	write(t, filepath.Join(entry, "HEAD"), "ref: refs/heads/"+branch+"\n")
	write(t, filepath.Join(dir, ".git"), "gitdir: "+entry+"\n")
	write(t, filepath.Join(dir, "f.txt"), "uncommitted\n")
}

func TestWorktreesOnGitsTwoWayLinkOnly(t *testing.T) {
	root, w := repo(t)
	worktree(t, w, "fix-a", filepath.Join(w, ".worktrees", "fix-a"), "fix/a")
	worktree(t, w, "spec-b", filepath.Join(w, ".worktrees", "spec-b"), "spec/b")
	worktree(t, w, "outside", filepath.Join(root, "elsewhere"), "x")

	// A forged gitdir naming a tracked directory: W/cmd has no .git file.
	write(t, filepath.Join(w, ".git", "worktrees", "forged", "gitdir"), filepath.Join(w, "cmd", ".git")+"\n")
	// A .git file that points at another entry.
	worktree(t, w, "other", filepath.Join(w, ".worktrees", "other"), "o")
	write(t, filepath.Join(w, ".worktrees", "other", ".git"), "gitdir: "+filepath.Join(w, ".git", "worktrees", "fix-a")+"\n")
	// A link on the way to D.
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(w, "via")); err != nil {
		t.Fatal(err)
	}
	worktree(t, w, "linked", filepath.Join(w, "via", "lw"), "l")
	// A link inside W to a directory inside W: D resolves below W, but not to itself.
	if err := os.MkdirAll(filepath.Join(w, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(w, "sub"), filepath.Join(w, "via2")); err != nil {
		t.Fatal(err)
	}
	worktree(t, w, "linked2", filepath.Join(w, "via2", "lw"), "l2")
	// D/.git a link to a file that names the entry back: not a regular file.
	worktree(t, w, "gitlink", filepath.Join(w, ".worktrees", "gitlink"), "gl")
	gl := filepath.Join(w, ".worktrees", "gitlink", ".git")
	write(t, filepath.Join(root, "gitfile"), "gitdir: "+filepath.Join(w, ".git", "worktrees", "gitlink")+"\n")
	if err := os.Remove(gl); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "gitfile"), gl); err != nil {
		t.Fatal(err)
	}
	// A gitdir naming W itself.
	write(t, filepath.Join(w, ".git", "worktrees", "self", "gitdir"), filepath.Join(w, ".git")+"\n")
	// One inside W/.git, with a .git file pointing back: never removed.
	worktree(t, w, "ingit", filepath.Join(w, ".git", "hooks", "x"), "g")

	p, err := PlanFor(Options{Project: "demo", WorkingDir: w, Files: true})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, wt := range p.Worktrees {
		got = append(got, wt.Rel+" ("+wt.Branch+")")
	}
	if want := []string{".worktrees/fix-a (fix/a)", ".worktrees/spec-b (spec/b)"}; !slices.Equal(got, want) {
		t.Errorf("worktrees %v, want %v", got, want)
	}
	if len(p.NotWorktrees) != 8 {
		t.Errorf("kept %v, want the 8 that are not worktrees of W", p.NotWorktrees)
	}

	done := Apply(Options{Project: "demo", WorkingDir: w, Files: true})
	if len(done.Errs) > 0 {
		t.Fatal(done.Errs)
	}
	for _, gone := range []string{".worktrees/fix-a", ".worktrees/spec-b", ".git/worktrees/fix-a", ".git/worktrees/spec-b"} {
		if exists(filepath.Join(w, gone)) {
			t.Errorf("%s is still there", gone)
		}
	}
	for _, kept := range []string{"cmd/main.go", ".git/HEAD", ".worktrees/other/f.txt", ".git/hooks/x/f.txt", ".git/worktrees/forged", "sub/lw/f.txt", ".worktrees/gitlink/f.txt"} {
		if !exists(filepath.Join(w, kept)) {
			t.Errorf("%s was removed", kept)
		}
	}
	for _, kept := range []string{"elsewhere/f.txt", "real/lw/f.txt"} {
		if !exists(filepath.Join(root, kept)) {
			t.Errorf("%s was removed", kept)
		}
	}
}

// Nothing runs git: a repository config naming a program never leaves its mark.
func TestWorktreesRunNoGit(t *testing.T) {
	root, w := repo(t)
	mark := filepath.Join(root, "ran")
	write(t, filepath.Join(w, ".git", "config"), "[core]\n\tfsmonitor = touch "+mark+"\n")
	worktree(t, w, "a", filepath.Join(w, ".worktrees", "a"), "a")
	Apply(Options{Project: "demo", WorkingDir: w, Files: true})
	if exists(mark) {
		t.Error("a program from the repository's config ran")
	}
}

func TestTemplateGoesALinkOnly(t *testing.T) {
	root, _ := repo(t)
	target := filepath.Join(root, "store", "demo.toml")
	write(t, target, "working_dir = \"/x\"\n")
	tmpl := filepath.Join(root, "projects", "demo.toml")
	if err := os.MkdirAll(filepath.Dir(tmpl), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, tmpl); err != nil {
		t.Fatal(err)
	}
	roles := filepath.Join(root, "roles.toml")
	write(t, roles, "[roles]\n")
	Apply(Options{Project: "demo", Template: tmpl, Files: true})
	if exists(tmpl) || !exists(target) || !exists(roles) {
		t.Errorf("link gone %v, target kept %v, roles.toml kept %v", !exists(tmpl), exists(target), exists(roles))
	}
	plain := filepath.Join(root, "projects", "other.toml")
	write(t, plain, "")
	Apply(Options{Project: "other", Template: plain, Files: true})
	if exists(plain) {
		t.Error("the template stayed")
	}
}

// A template not named <project>.toml is never removed.
func TestTemplateMustBeTheProjects(t *testing.T) {
	root, _ := repo(t)
	p := filepath.Join(root, "projects", "other.toml")
	write(t, p, "")
	plan, _ := PlanFor(Options{Project: "demo", Template: p, Files: true})
	Apply(Options{Project: "demo", Template: p, Files: true})
	if plan.Template != "" || !exists(p) {
		t.Errorf("another project's template planned %q, still there %v", plan.Template, exists(p))
	}
}

func archive(t *testing.T, dir string, at time.Time, projects ...string) string {
	t.Helper()
	var b bytes.Buffer
	w, err := backup.NewWriter(&b, backup.Coverage{Format: backup.Format, CreatedAt: at, Host: "box", Projects: projects})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(backup.Manifest{}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, backup.ArchiveName("box", at))
	write(t, p, b.String())
	return p
}

func TestBackupsOfTheProjectAloneTheFreshOneKept(t *testing.T) {
	root, _ := repo(t)
	dir := filepath.Join(root, "backups")
	t0 := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	old := archive(t, dir, t0, "demo")
	older := archive(t, dir, t0.Add(-time.Hour), "demo")
	shared := archive(t, dir, t0.Add(-2*time.Hour), "demo", "other")
	foreign := archive(t, dir, t0.Add(-3*time.Hour), "other")
	fresh := archive(t, dir, t0.Add(time.Minute), "demo")
	bad := filepath.Join(dir, backup.ArchiveName("box", t0.Add(-4*time.Hour)))
	write(t, bad, "not an archive")
	link := filepath.Join(root, "fresh-link.tar.zst")
	if err := os.Symlink(fresh, link); err != nil {
		t.Fatal(err)
	}

	o := Options{Project: "demo", BackupDir: dir, Host: "box", Backups: true, KeepArchive: link}
	p, err := PlanFor(o)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Archives, []string{old, older}) || p.Shared != 1 || len(p.Unreadable) != 1 || p.Kept != fresh {
		t.Fatalf("plan %+v", p)
	}
	done := Apply(o)
	for _, f := range []string{old, older} {
		if exists(f) {
			t.Errorf("%s stayed", filepath.Base(f))
		}
	}
	for _, f := range []string{shared, foreign, fresh, bad} {
		if !exists(f) {
			t.Errorf("%s was removed", filepath.Base(f))
		}
	}
	if len(done.Archives) != 2 {
		t.Errorf("done %+v", done)
	}

	// With no backup made (n), nothing is exempt.
	o.KeepArchive = ""
	Apply(o)
	if exists(fresh) {
		t.Error("with no archive made now, the project's last one stayed")
	}
}

func TestBothOffTouchNothing(t *testing.T) {
	root, w := repo(t)
	worktree(t, w, "a", filepath.Join(w, ".worktrees", "a"), "a")
	tmpl := filepath.Join(root, "projects", "demo.toml")
	write(t, tmpl, "")
	dir := filepath.Join(root, "backups")
	a := archive(t, dir, time.Now(), "demo")
	o := Options{Project: "demo", WorkingDir: w, Template: tmpl, BackupDir: dir, Host: "box"}
	p, _ := PlanFor(o)
	Apply(o)
	if !p.Empty() || !exists(tmpl) || !exists(a) || !exists(filepath.Join(w, ".worktrees", "a")) {
		t.Errorf("both off touched something: %+v", p)
	}
}

func TestPlanLines(t *testing.T) {
	p := Plan{Template: "/h/.config/agentbus/projects/demo.toml",
		Worktrees: []Worktree{{Rel: ".worktrees/fix-a", Branch: "fix/a"}},
		Archives:  []string{"a", "b"}, Shared: 3, Kept: "/b/new.tar.zst"}
	got := strings.Join(p.Lines("demo"), "\n")
	for _, want := range []string{"template /h/.config/agentbus/projects/demo.toml",
		"worktrees: .worktrees/fix-a (fix/a): uncommitted changes in them are lost",
		"backups: 2 archives of demo alone; 3 archives also hold other projects: kept",
		"kept: /b/new.tar.zst (made now)"} {
		if !strings.Contains(got, want) {
			t.Errorf("lines\n%s\nlack %q", got, want)
		}
	}
}
