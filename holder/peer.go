package holder

import (
	"fmt"
	"net"
	"os"

	"github.com/netbja/agent-bus-monitor/dispatch"
)

// Peer is the process at the other end of a unix socket, as the kernel
// reports it (SO_PEERCRED on Linux; LOCAL_PEERPID and LOCAL_PEERCRED, the
// getpeereid data, on macOS).
type Peer struct {
	PID, UID int
}

// PeerOf reads c's peer from the kernel.
func PeerOf(c *net.UnixConn) (Peer, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return Peer{}, err
	}
	var p Peer
	var perr error
	if err := raw.Control(func(fd uintptr) { p, perr = peerOfFD(int(fd)) }); err != nil {
		return Peer{}, err
	}
	return p, perr
}

// CheckSameUser refuses a peer of another uid (spec 4.4, security S6): the
// holder checks its client, and a client checks the holder before it sends
// a Start or a single attach byte. It is mutual.
func CheckSameUser(p Peer) error {
	if p.UID != os.Getuid() {
		return fmt.Errorf("holder: the socket's peer (pid %d) runs as uid %d, another user: refused", p.PID, p.UID)
	}
	return nil
}

// MaxTreeDepth bounds the ancestor walk of the in-tree check (spec 4.2).
const MaxTreeDepth = 32

// InTree reports whether the process peerPID runs inside one of the
// holder's terminals (security S2b): one of its ancestors is the holder,
// matched by pid AND start time (a reused pid has another start). The walk
// is bounded: a chain that does not end at pid 1 within MaxTreeDepth
// ancestors (too deep, or a process that could not be read, as on a
// platform with no ancestry yet) counts as in-tree, so an unknown is
// refused the holder's writes, never let through as outside. With the
// holder a subreaper (Linux, step 5b), an orphan or a double-forked process
// stays its descendant.
func InTree(peerPID int, holder dispatch.ProcID, ancestorsOf func(int) []dispatch.ProcID) bool {
	chain := ancestorsOf(peerPID)
	if len(chain) == 0 || len(chain) > MaxTreeDepth+1 || chain[len(chain)-1].PID != 1 {
		return true
	}
	for _, a := range chain {
		if a.PID == holder.PID && a.Start == holder.Start {
			return true
		}
	}
	return false
}
