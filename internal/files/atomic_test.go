package files

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// noTemps fails when a temporary file is left beside the target.
func noTemps(t *testing.T, dir string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.Name() != "state.json" {
			t.Errorf("left behind in %s: %s", dir, e.Name())
		}
	}
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// A new file is 0600 in a 0700 directory created for it.
func TestWriteAtomicCreatesFileAndDirectoryWithTheirModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	p := filepath.Join(dir, "state.json")
	if err := WriteAtomic(p, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "one" {
		t.Errorf("content %q", b)
	}
	if m := mode(t, p); m != 0o600 {
		t.Errorf("file mode %v, want 0600", m)
	}
	if m := mode(t, dir); m != 0o700 {
		t.Errorf("directory mode %v, want 0700", m)
	}
	noTemps(t, dir)
}

// Replacing a file readable by others gives the new file 0600: the mode is the
// writer's, not the old file's.
func TestWriteAtomicReplacesWithMode0600(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(p, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "new" {
		t.Errorf("content %q", b)
	}
	if m := mode(t, p); m != 0o600 {
		t.Errorf("file mode %v, want 0600", m)
	}
	noTemps(t, dir)
}

// The temporary file is synced before the rename, and the directory after it:
// the content reaches the disk before the name points at it, and the name
// survives a crash.
func TestWriteAtomicSyncsFileBeforeRenameAndDirectoryAfter(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	oldFile, oldDir := syncFile, syncDir
	t.Cleanup(func() { syncFile, syncDir = oldFile, oldDir })
	syncFile = func(f *os.File) error {
		b, _ := os.ReadFile(p)
		calls = append(calls, "file:"+filepath.Dir(f.Name())+":"+string(b))
		return f.Sync()
	}
	syncDir = func(d string) error {
		b, _ := os.ReadFile(p)
		calls = append(calls, "dir:"+d+":"+string(b))
		return oldDir(d)
	}
	if err := WriteAtomic(p, []byte("new")); err != nil {
		t.Fatal(err)
	}
	want := []string{"file:" + dir + ":old", "dir:" + dir + ":new"}
	if len(calls) != 2 || calls[0] != want[0] || calls[1] != want[1] {
		t.Errorf("syncs %q, want %q", calls, want)
	}
}

// Every failure before the rename leaves the old file whole and no temporary
// file behind.
func TestWriteAtomicFailureLeavesTheOldFile(t *testing.T) {
	boom := errors.New("boom")
	for _, c := range []struct {
		name string
		set  func()
	}{
		{"file sync", func() { syncFile = func(*os.File) error { return boom } }},
		{"write", func() { writeFile = func(*os.File, []byte) (int, error) { return 0, boom } }},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "state.json")
			if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			oldFile, oldWrite := syncFile, writeFile
			t.Cleanup(func() { syncFile, writeFile = oldFile, oldWrite })
			c.set()
			if err := WriteAtomic(p, []byte("new")); !errors.Is(err, boom) {
				t.Fatalf("err %v, want %v", err, boom)
			}
			if b, _ := os.ReadFile(p); string(b) != "old" {
				t.Errorf("content %q, want the old file", b)
			}
			noTemps(t, dir)
		})
	}
}

// A failed directory sync is reported: the new content is in place, but the
// caller must not take it as durable.
func TestWriteAtomicReportsADirectorySyncFailure(t *testing.T) {
	boom := errors.New("boom")
	old := syncDir
	t.Cleanup(func() { syncDir = old })
	syncDir = func(string) error { return boom }
	dir := t.TempDir()
	if err := WriteAtomic(filepath.Join(dir, "state.json"), []byte("x")); !errors.Is(err, boom) {
		t.Fatalf("err %v, want %v", err, boom)
	}
	noTemps(t, dir)
}

// A directory that cannot be made (a file where it should be) is an error,
// and nothing is written.
func TestWriteAtomicUnderAFileFails(t *testing.T) {
	dir := t.TempDir()
	block := filepath.Join(dir, "state.json")
	if err := os.WriteFile(block, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(filepath.Join(block, "sub", "x.json"), []byte("x")); err == nil {
		t.Fatal("writing under a file must fail")
	}
	noTemps(t, dir)
}

// The rename target is the path itself: a symlink there is replaced, not
// followed (a caller that edits through a link resolves it first, as config
// does).
func TestWriteAtomicReplacesASymlinkNotItsTarget(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.WriteFile(real, []byte("real"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "state.json")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(link, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(real); string(b) != "real" {
		t.Errorf("the link's target changed: %q", b)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink != 0 {
		t.Error("the path is still a link")
	}
}
