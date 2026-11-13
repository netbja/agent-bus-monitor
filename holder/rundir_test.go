package holder

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// The run directory (spec 4.4, security S6): $XDG_RUNTIME_DIR, else
// $TMPDIR/agentbus-<uid>, else /tmp/agentbus-<uid>; every level created
// 0700, then Lstat'ed: no symlink, our uid, exactly 0700.
func TestProjectDirCreatesAndChecks(t *testing.T) {
	xdg := t.TempDir()
	if err := os.Chmod(xdg, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := ProjectDir(envOf(map[string]string{"XDG_RUNTIME_DIR": xdg}), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(xdg, "agentbus", "demo") {
		t.Fatalf("dir %q", dir)
	}
	for _, d := range []string{filepath.Join(xdg, "agentbus"), dir} {
		st, err := os.Lstat(d)
		if err != nil || st.Mode().Perm() != 0o700 || !st.IsDir() {
			t.Fatalf("%s: %v %v", d, st.Mode(), err)
		}
	}
	// Again: the existing levels pass.
	if _, err := ProjectDir(envOf(map[string]string{"XDG_RUNTIME_DIR": xdg}), "demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectDir(envOf(map[string]string{"XDG_RUNTIME_DIR": xdg}), "Bad Name"); err == nil {
		t.Fatal("an invalid project accepted")
	}
}

func TestRunDirFallbacks(t *testing.T) {
	uid := strconv.Itoa(os.Getuid())
	tmp := t.TempDir()
	if got := runDir(envOf(map[string]string{"TMPDIR": tmp})); got.path != filepath.Join(tmp, "agentbus-"+uid) || !got.fallback {
		t.Fatalf("TMPDIR fallback %+v", got)
	}
	if got := runDir(envOf(nil)); got.path != filepath.Join("/tmp", "agentbus-"+uid) || !got.fallback {
		t.Fatalf("/tmp fallback %+v", got)
	}
	if got := runDir(envOf(map[string]string{"XDG_RUNTIME_DIR": "/run/user/1", "TMPDIR": tmp})); got.path != "/run/user/1" || got.fallback {
		t.Fatalf("XDG %+v", got)
	}
	// The fallback level itself is created and checked.
	dir, err := ProjectDir(envOf(map[string]string{"TMPDIR": tmp}), "demo")
	if err != nil || dir != filepath.Join(tmp, "agentbus-"+uid, "agentbus", "demo") {
		t.Fatalf("%q %v", dir, err)
	}
	if st, _ := os.Lstat(filepath.Join(tmp, "agentbus-"+uid)); st.Mode().Perm() != 0o700 {
		t.Fatalf("fallback level mode %v", st.Mode())
	}
}

// A symlink at any level, a wider mode: refused, under XDG_RUNTIME_DIR and in
// the fallback, with nothing followed.
func TestProjectDirRefuses(t *testing.T) {
	uid := strconv.Itoa(os.Getuid())
	type setup func(t *testing.T) (env map[string]string, why string)
	cases := map[string]setup{
		"a symlink for agentbus (xdg)": func(t *testing.T) (map[string]string, string) {
			xdg, other := t.TempDir(), t.TempDir()
			os.Chmod(xdg, 0o700)
			os.Chmod(other, 0o700)
			if err := os.Symlink(other, filepath.Join(xdg, "agentbus")); err != nil {
				t.Fatal(err)
			}
			return map[string]string{"XDG_RUNTIME_DIR": xdg}, "symlink"
		},
		"a symlink for the project (xdg)": func(t *testing.T) (map[string]string, string) {
			xdg, other := t.TempDir(), t.TempDir()
			os.Chmod(xdg, 0o700)
			os.Chmod(other, 0o700)
			os.Mkdir(filepath.Join(xdg, "agentbus"), 0o700)
			os.Symlink(other, filepath.Join(xdg, "agentbus", "demo"))
			return map[string]string{"XDG_RUNTIME_DIR": xdg}, "symlink"
		},
		"a symlink for the fallback level": func(t *testing.T) (map[string]string, string) {
			tmp, other := t.TempDir(), t.TempDir()
			os.Chmod(other, 0o700)
			os.Symlink(other, filepath.Join(tmp, "agentbus-"+uid))
			return map[string]string{"TMPDIR": tmp}, "symlink"
		},
		"a wider mode (xdg)": func(t *testing.T) (map[string]string, string) {
			xdg := t.TempDir()
			os.Chmod(xdg, 0o700)
			os.Mkdir(filepath.Join(xdg, "agentbus"), 0o700)
			os.Chmod(filepath.Join(xdg, "agentbus"), 0o755)
			return map[string]string{"XDG_RUNTIME_DIR": xdg}, "mode 0755"
		},
		"a wider mode (fallback)": func(t *testing.T) (map[string]string, string) {
			tmp := t.TempDir()
			os.Mkdir(filepath.Join(tmp, "agentbus-"+uid), 0o700)
			os.Chmod(filepath.Join(tmp, "agentbus-"+uid), 0o711)
			return map[string]string{"TMPDIR": tmp}, "mode 0711"
		},
		"a narrower mode (foureyes, 5a)": func(t *testing.T) (map[string]string, string) {
			xdg := t.TempDir()
			os.Chmod(xdg, 0o700)
			os.Mkdir(filepath.Join(xdg, "agentbus"), 0o700)
			os.Chmod(filepath.Join(xdg, "agentbus"), 0o500)
			t.Cleanup(func() { os.Chmod(filepath.Join(xdg, "agentbus"), 0o700) })
			return map[string]string{"XDG_RUNTIME_DIR": xdg}, "mode 0500"
		},
		"a file, not a directory": func(t *testing.T) (map[string]string, string) {
			xdg := t.TempDir()
			os.Chmod(xdg, 0o700)
			os.WriteFile(filepath.Join(xdg, "agentbus"), nil, 0o700)
			return map[string]string{"XDG_RUNTIME_DIR": xdg}, "not a directory"
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			env, why := c(t)
			if _, err := ProjectDir(envOf(env), "demo"); err == nil || !strings.Contains(err.Error(), why) {
				t.Fatalf("err %v, want %q", err, why)
			}
		})
	}
}

// The owner check, with an injected owner: another uid is refused with the
// spec's words.
func TestCheckLevelOwner(t *testing.T) {
	d := t.TempDir()
	os.Chmod(d, 0o700)
	st, err := os.Lstat(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkLevel(d, st, os.Getuid()+1); err == nil || !strings.Contains(err.Error(), "another user created it; remove it or set TMPDIR") ||
		!strings.Contains(err.Error(), "owned by uid "+strconv.Itoa(os.Getuid())+", not you") {
		t.Fatalf("err %v", err)
	}
	if err := checkLevel(d, st, os.Getuid()); err != nil {
		t.Fatal(err)
	}
}

// The socket path: <dir>/holder-<key>.sock, refused past 104 bytes (macOS's
// sun_path) and for an invalid build key.
func TestSocketPath(t *testing.T) {
	xdg := t.TempDir()
	os.Chmod(xdg, 0o700)
	p, err := SocketPath(envOf(map[string]string{"XDG_RUNTIME_DIR": xdg}), "demo", "v1.0123456789")
	if err != nil || p != filepath.Join(xdg, "agentbus", "demo", "holder-v1.0123456789.sock") {
		t.Fatalf("%q %v", p, err)
	}
	deep := filepath.Join(xdg, strings.Repeat("d", 80))
	os.Mkdir(deep, 0o700)
	if _, err := SocketPath(envOf(map[string]string{"XDG_RUNTIME_DIR": deep}), "demo", "v1.0123456789"); err == nil || !strings.Contains(err.Error(), "104") {
		t.Fatalf("a long path: %v", err)
	}
	if _, err := SocketPath(envOf(map[string]string{"XDG_RUNTIME_DIR": xdg}), "demo", "../x"); err == nil {
		t.Fatal("an invalid build key accepted")
	}
}
