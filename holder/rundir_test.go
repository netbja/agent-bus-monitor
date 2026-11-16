package holder

import (
	"os"
	"path/filepath"
	"runtime"
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

// The socket path: <dir>/holder-<key>.sock, refused past this OS's sun_path
// and for an invalid build key.
func TestSocketPath(t *testing.T) {
	xdg := t.TempDir()
	os.Chmod(xdg, 0o700)
	p, err := SocketPath(envOf(map[string]string{"XDG_RUNTIME_DIR": xdg}), "demo", "v1.0123456789")
	if err != nil || p != filepath.Join(xdg, "agentbus", "demo", "h-"+socketID("v1.0123456789")+".sock") {
		t.Fatalf("%q %v", p, err)
	}
	deep := filepath.Join(xdg, strings.Repeat("d", 80))
	os.Mkdir(deep, 0o700)
	if _, err := SocketPath(envOf(map[string]string{"XDG_RUNTIME_DIR": deep}), "demo", "v1.0123456789"); err == nil || !strings.Contains(err.Error(), "a unix socket path takes") {
		t.Fatalf("a long path: %v", err)
	}
	if _, err := SocketPath(envOf(map[string]string{"XDG_RUNTIME_DIR": xdg}), "demo", "../x"); err == nil {
		t.Fatal("an invalid build key accepted")
	}
}

// fieldKey is the build key of the field failure (P0-2, 2026-11-16): a dirty
// build's key, its socket path 105 bytes long under the legacy name
// holder-<key>.sock. xdgFor pads for the current name, h-<id>.sock, so the
// limits below are judged on the path a holder of this build listens on.
const fieldKey = "v0.28.0.877.g1691ae95.1691ae950206.dirty.c7ab19e304"

// xdgFor is a 0700 run directory whose socket path for project and key is
// exactly n bytes long.
func xdgFor(t *testing.T, project, key string, n int) string {
	t.Helper()
	base, err := os.MkdirTemp("/tmp", "h") // short: t.TempDir() is too long to build a 105-byte path in
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	tail := len("/agentbus/" + project + "/" + socketName(key))
	pad := n - tail - len(base) - 1
	if pad < 1 {
		t.Skipf("the temp dir %s is too long for a %d-byte path", base, n)
	}
	x := filepath.Join(base, strings.Repeat("x", pad))
	if err := os.Mkdir(x, 0o700); err != nil {
		t.Fatal(err)
	}
	return x
}

// The field failure: a 105-byte socket path was refused on Linux by macOS's
// limit, so the holder never listened and the dispatcher waited for it. Each
// OS has its own sun_path: Linux 108, macOS 104, the NUL included.
func TestSocketPathFieldFailure(t *testing.T) {
	if got := maxSocketPathFor("linux"); got != 107 {
		t.Fatalf("linux %d", got)
	}
	if got := maxSocketPathFor("darwin"); got != 103 {
		t.Fatalf("darwin %d", got)
	}
	if got := maxSocketPathFor("freebsd"); got != 103 {
		t.Fatalf("another OS: the smallest, %d", got)
	}
	if runtime.GOOS != "linux" {
		t.Skip("the field failure is Linux's")
	}
	x := xdgFor(t, "abm", fieldKey, 105)
	env := envOf(map[string]string{"XDG_RUNTIME_DIR": x})
	p, err := SocketPath(env, "abm", fieldKey)
	if err != nil || len(p) != 105 {
		t.Fatalf("the field's 105-byte path: %d %v", len(p), err)
	}
	if err := SocketPathFits(env, "abm", fieldKey); err != nil {
		t.Fatalf("fits: %v", err)
	}
	edge := xdgFor(t, "abm", fieldKey, 107) // the longest Linux takes
	if p, err := SocketPath(envOf(map[string]string{"XDG_RUNTIME_DIR": edge}), "abm", fieldKey); err != nil || len(p) != 107 {
		t.Fatalf("a 107-byte path: %d %v", len(p), err)
	}
	long := xdgFor(t, "abm", fieldKey, 108)
	if _, err := SocketPath(envOf(map[string]string{"XDG_RUNTIME_DIR": long}), "abm", fieldKey); err == nil || !strings.Contains(err.Error(), "108") {
		t.Fatalf("a 108-byte path on Linux: %v", err)
	}
}

// SocketPathFits judges the length without creating any directory: agentbus
// up asks it before it starts anything.
func TestSocketPathFitsCreatesNothing(t *testing.T) {
	x := filepath.Join(t.TempDir(), "run")
	if err := SocketPathFits(envOf(map[string]string{"XDG_RUNTIME_DIR": x}), "demo", "v1.0123456789"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(x); !os.IsNotExist(err) {
		t.Fatalf("a directory was created: %v", err)
	}
	deep := filepath.Join(x, strings.Repeat("d", 120))
	if err := SocketPathFits(envOf(map[string]string{"XDG_RUNTIME_DIR": deep}), "demo", "v1.0123456789"); err == nil || !strings.Contains(err.Error(), "XDG_RUNTIME_DIR") {
		t.Fatalf("too long: %v", err)
	}
	if err := SocketPathFits(envOf(map[string]string{"XDG_RUNTIME_DIR": x}), "Bad", "v1.0123456789"); err == nil {
		t.Fatal("an invalid project accepted")
	}
	if err := SocketPathFits(envOf(map[string]string{"XDG_RUNTIME_DIR": x}), "demo", "../x"); err == nil {
		t.Fatal("an invalid build key accepted")
	}
}

// The socket's name is bounded whatever the build key (P0-2 B): h-<16 hex
// of the key's sha256>.sock, 23 bytes, so a long dirty key and a 32-byte
// project fit under /run/user/<uid>.
func TestSocketNameIsBounded(t *testing.T) {
	long := "v0.28.0.877.g1691ae95.1691ae950206.dirty.c7ab19e304"
	if got := socketID(long); len(got) != 16 || got == socketID("v1.0123456789") {
		t.Fatalf("id %q", got)
	}
	if socketID(long) != socketID(long) {
		t.Fatal("not deterministic")
	}
	xdg, err := os.MkdirTemp("/tmp", "h") // short: t.TempDir() is too long here
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(xdg) })
	os.Chmod(xdg, 0o700)
	project := strings.Repeat("p", 32)
	p, err := SocketPath(envOf(map[string]string{"XDG_RUNTIME_DIR": xdg}), project, long)
	if err != nil || filepath.Base(p) != "h-"+socketID(long)+".sock" || len(filepath.Base(p)) != 23 {
		t.Fatalf("%q %v", p, err)
	}
	// /run/user/1000 (14 bytes) + /agentbus/ + 32 + / + 23 = 80: far under either OS's limit.
	if n := len("/run/user/1000/agentbus/" + project + "/" + filepath.Base(p)); n > 103 {
		t.Fatalf("%d bytes", n)
	}
}

// ProjectSockets reads both formats (P0-2 B): the legacy holder-<key>.sock,
// whose name holds its key (an older build still running after an
// upgrade), and h-<id>.sock beside the h-<id>.key its holder wrote. A new
// socket whose key file is missing, names another id's key, holds an
// invalid key, or is not a regular file is left out.
func TestProjectSockets(t *testing.T) {
	dir := t.TempDir()
	touch := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	touch("holder-v1.0123456789.sock", "")
	touch("h-"+socketID("v2.0123456789")+".sock", "")
	touch("h-"+socketID("v2.0123456789")+".key", "v2.0123456789\n")
	touch("h-"+socketID("v3.0123456789")+".sock", "") // no key file
	touch("h-"+socketID("v4.0123456789")+".sock", "")
	touch("h-"+socketID("v4.0123456789")+".key", "v5.0123456789") // another id's key
	touch("h-"+socketID("v6.0123456789")+".sock", "")
	touch("h-"+socketID("v6.0123456789")+".key", "../x") // not a key
	touch("h-"+socketID("v7.0123456789")+".sock", "")
	elsewhere := filepath.Join(t.TempDir(), "k")
	os.WriteFile(elsewhere, []byte("v7.0123456789"), 0o600)                          // its own key, through a symlink
	os.Symlink(elsewhere, filepath.Join(dir, "h-"+socketID("v7.0123456789")+".key")) // not a regular file
	touch("h-"+socketID("Bad Key")+".sock", "")
	touch("h-"+socketID("Bad Key")+".key", "Bad Key") // its id, but not a build key
	touch("h-"+socketID("v8.0123456789")+".sock", "")
	touch("h-"+socketID("v8.0123456789")+".key", "v8.0123456789")
	touch("holder-v8.0123456789.sock", "") // both names for one key: the new one wins
	touch("holder-bad key.sock", "")
	touch("notes.txt", "")
	got, err := ProjectSockets(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"v1.0123456789": filepath.Join(dir, "holder-v1.0123456789.sock"),
		"v2.0123456789": filepath.Join(dir, "h-"+socketID("v2.0123456789")+".sock"),
		"v8.0123456789": filepath.Join(dir, "h-"+socketID("v8.0123456789")+".sock"),
	}
	if len(got) != len(want) || got["v1.0123456789"] != want["v1.0123456789"] || got["v2.0123456789"] != want["v2.0123456789"] ||
		got["v8.0123456789"] != want["v8.0123456789"] {
		t.Fatalf("got %v, want %v", got, want)
	}
	if empty, err := ProjectSockets(filepath.Join(dir, "none")); err != nil || len(empty) != 0 {
		t.Fatalf("a missing dir: %v %v", empty, err)
	}
}

// SocketFor resolves a key's socket: the new name when it is there, else
// the legacy one when that is there, else the new one (where a holder of
// this build will listen).
func TestSocketFor(t *testing.T) {
	xdg := filepath.Join(t.TempDir(), "r")
	os.Mkdir(xdg, 0o700)
	env := envOf(map[string]string{"XDG_RUNTIME_DIR": xdg})
	dir, err := ProjectDir(env, "demo")
	if err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(dir, "h-"+socketID("v1.0123456789")+".sock")
	if p, err := SocketFor(env, "demo", "v1.0123456789"); err != nil || p != fresh {
		t.Fatalf("nothing there: %q %v", p, err)
	}
	legacy := filepath.Join(dir, "holder-v1.0123456789.sock")
	os.WriteFile(legacy, nil, 0o600)
	if p, _ := SocketFor(env, "demo", "v1.0123456789"); p != legacy {
		t.Fatalf("legacy only: %q", p)
	}
	os.WriteFile(fresh, nil, 0o600)
	if p, _ := SocketFor(env, "demo", "v1.0123456789"); p != fresh {
		t.Fatalf("both: %q", p)
	}
}
