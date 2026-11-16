package setup

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRunner runs the commands for real: the regression test needs git's own
// view of an untracked file.
type gitRunner struct{}

func (gitRunner) Run(ctx context.Context, c Cmd) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	return cmd.CombinedOutput()
}

func (gitRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

// build-id-untracked (2026-11-16): Go's vcs.modified counts untracked files,
// so the person's notes in the repository root made every setup build
// "+dirty", whose longer build key put the holder's socket path over its
// limit and the holder never started. The tree setup stamps is git's
// tracked view: an untracked file leaves it clean, a modified tracked file
// makes it dirty, and setup says so.
func TestTreeIgnoresUntrackedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "tracked.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "tracked.go")
	git("commit", "-q", "-m", "one")
	if err := os.WriteFile(filepath.Join(repo, "my-notes.md"), []byte("the person's notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	in := &Installer{R: gitRunner{}, O: NewOutput(&out, &out, false), Repo: repo}
	ctx := context.Background()
	if rev := in.RepoRev(ctx); Tree(rev) != "clean" {
		t.Fatalf("an untracked file: rev %q reads %q", rev, Tree(rev))
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.go"), []byte("package x // changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rev := in.RepoRev(ctx); Tree(rev) != "dirty" {
		t.Fatalf("a modified tracked file: rev %q reads %q", rev, Tree(rev))
	}
}

// The tree of a revision setup could not read is unknown: "" (the binary
// then falls back to Go's own vcs.modified).
func TestTreeOfAnUnknownRevision(t *testing.T) {
	for rev, want := range map[string]string{"v1.2.3": "clean", "v1.2.3-4-gabc-dirty": "dirty", "abc1234": "clean", "abc1234-dirty": "dirty", "unknown revision": "", "": ""} {
		if got := Tree(rev); got != want {
			t.Errorf("Tree(%q) = %q, want %q", rev, got, want)
		}
	}
}

// A dirty tree is stamped and said, once for the whole install; a clean one
// is stamped and not said.
func TestBuildSaysADirtyTree(t *testing.T) {
	for _, c := range []struct {
		rev  string
		warn bool
	}{{"v9.9.9-1-gabc-dirty", true}, {"v9.9.9", false}} {
		in, out, _, _, _ := buildWorld(t, c.rev, nil)
		ctx := context.Background()
		in.Rev = in.RepoRev(ctx)
		for _, b := range [][2]string{{"agentbus", "./cmd/agentbus"}, {"busmon", "./cmd/busmon"}} {
			if err := in.BuildAndInstall(ctx, b[0], b[1]); err != nil {
				t.Fatal(err)
			}
		}
		said := strings.Count(out.String(), "modified tracked files")
		if c.warn && (said != 1 || in.O.Warnings != 1) || !c.warn && said != 0 {
			t.Errorf("%s: said %d times, %d warnings:\n%s", c.rev, said, in.O.Warnings, out.String())
		}
	}
}
