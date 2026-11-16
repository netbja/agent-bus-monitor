package holder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/service"
)

// A holder listening at h-<id>.sock writes h-<id>.key (0600, its key), so a
// reader maps the socket to its build (P0-2 B); its shutdown removes both.
func TestHolderWritesItsKeyFile(t *testing.T) {
	s, sock := newTestHolder(t)
	key := service.BuildKey("test")
	kf := strings.TrimSuffix(sock, ".sock") + ".key"
	st, err := os.Lstat(kf)
	if err != nil || st.Mode().Perm() != 0o600 || !st.Mode().IsRegular() {
		t.Fatalf("key file %v %v", st, err)
	}
	if b, _ := os.ReadFile(kf); strings.TrimSpace(string(b)) != key {
		t.Fatalf("key file holds %q", b)
	}
	socks, err := ProjectSockets(strings.TrimSuffix(sock, "/"+socketName(key)))
	if err != nil || socks[key] != sock {
		t.Fatalf("ProjectSockets %v %v", socks, err)
	}
	s.Shutdown()
	for _, p := range []string{sock, kf} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("%s left after shutdown: %v", p, err)
		}
	}
}

// An older build's holder keeps its legacy holder-<key>.sock after an
// upgrade: the new build's Host still finds it (Describe, as draining) and
// reaches it (List), so it drains (P0-2 B, master: old-format readers keep
// working).
func TestHostReachesALegacyNamedHolder(t *testing.T) {
	dir, err := os.MkdirTemp("", "hx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	os.Chmod(dir, 0o700)
	getenv := func(k string) string {
		if k == "XDG_RUNTIME_DIR" {
			return dir
		}
		return ""
	}
	pdir, err := ProjectDir(getenv, "demo")
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(pdir, legacySocketName(service.BuildKey("test")))
	s, err := NewServer(Config{Project: "demo", Build: "test", Socket: legacy, Launch: testRoles(t), Grace: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	go s.Serve(context.Background())
	t.Cleanup(s.Shutdown)
	if _, err := os.Lstat(strings.TrimSuffix(legacy, ".sock") + ".key"); !os.IsNotExist(err) {
		t.Fatalf("a legacy-named holder wrote a key file: %v", err)
	}
	h, err := NewHost("demo", "test-newer", getenv)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := h.Describe(ctx)
	if err != nil || len(info.Draining) != 1 || info.Draining[0] != "test" || len(info.Sockets) != 1 || info.Sockets[0] != legacy {
		t.Fatalf("describe %+v %v", info, err)
	}
	if _, err := h.List(ctx, "demo"); err != nil {
		t.Fatalf("list through the legacy socket: %v", err)
	}
	if files, err := h.SocketFiles(); err != nil || len(files) != 1 || files[0] != legacy {
		t.Fatalf("socket files %v %v", files, err)
	}
}
