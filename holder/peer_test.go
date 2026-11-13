package holder

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/dispatch"
)

// The peer of a unix socket (spec 4.4, security S2b and S6): its pid and its
// uid, read from the kernel, both ways (the holder reads its client, the
// client reads the holder).
func TestPeerOfAConnection(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "s")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan Peer, 1)
	go func() {
		c, err := ln.AcceptUnix()
		if err != nil {
			done <- Peer{}
			return
		}
		defer c.Close()
		p, _ := PeerOf(c)
		done <- p
	}()
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	server, err := PeerOf(c)
	if err != nil || server.UID != os.Getuid() || server.PID != os.Getpid() {
		t.Fatalf("server peer %+v %v", server, err)
	}
	if client := <-done; client.UID != os.Getuid() || client.PID != os.Getpid() {
		t.Fatalf("client peer %+v", client)
	}
	if err := CheckSameUser(server); err != nil {
		t.Fatal(err)
	}
	if err := CheckSameUser(Peer{PID: 1, UID: os.Getuid() + 1}); err == nil || !strings.Contains(err.Error(), "another user") {
		t.Fatalf("another uid: %v", err)
	}
}

// In-tree (security S2b): a peer one of whose ancestors is the holder,
// matched by pid AND start time, within 32 ancestors. A chain that does not
// end at pid 1 within the bound (unreadable, too deep) is in-tree: refused,
// never assumed outside.
func TestInTree(t *testing.T) {
	holder := dispatch.ProcID{PID: 500, Start: 99}
	chain := func(ids ...dispatch.ProcID) func(int) []dispatch.ProcID {
		return func(pid int) []dispatch.ProcID {
			if pid != 4242 {
				t.Fatalf("ancestors of %d", pid)
			}
			return ids
		}
	}
	p := func(pid int, start uint64) dispatch.ProcID { return dispatch.ProcID{PID: pid, Start: start} }
	init1 := p(1, 1)
	deep := func(n int, at int) []dispatch.ProcID {
		var ids []dispatch.ProcID
		for i := 0; i < n; i++ {
			ids = append(ids, p(1000+i, 7))
		}
		if at >= 0 {
			ids[at] = holder
		}
		return append(ids, init1)
	}
	for name, c := range map[string]struct {
		ids  []dispatch.ProcID
		want bool
	}{
		"a child of the holder":             {[]dispatch.ProcID{holder, init1}, true},
		"a grandchild through a shell":      {[]dispatch.ProcID{p(600, 3), holder, p(400, 2), init1}, true},
		"the 32nd ancestor":                 {deep(32, 31), true},
		"outside: the shell of the person":  {[]dispatch.ProcID{p(300, 5), p(200, 4), init1}, false},
		"the pid reused, another start":     {[]dispatch.ProcID{p(500, 98), init1}, false},
		"no ancestor read":                  {nil, true},
		"a chain cut before pid 1":          {[]dispatch.ProcID{p(300, 5), p(200, 4)}, true},
		"deeper than 32 (fail closed)":      {deep(40, -1), true},
		"the holder past the 32nd ancestor": {deep(40, 35), true},
	} {
		if got := InTree(4242, holder, chain(c.ids...)); got != c.want {
			t.Errorf("%s: in tree %v, want %v", name, got, c.want)
		}
	}
}
