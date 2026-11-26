package holder

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// Moved from cmd/agentbus unchanged (dispatchsvc P3): asking a project's
// holder of a build whether it answers and what it hosts.

// Ready is up's readiness probe: project's holder of build key
// completes its handshake within timeout. The welcome comes from the
// holder's accept loop, so a socket that only listens (its connections
// queued by the kernel) or accepts and stays silent is not ready
// (reviewer-codex).
func Ready(getenv func(string) string, project, key string, timeout time.Duration) bool {
	sock, err := SocketFor(getenv, project, key)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cl, err := DialContext(ctx, sock, BuildID())
	if err != nil {
		return false
	}
	cl.Close()
	return true
}

// TermList is the terminals of project's holder of build key.
func TermList(project, key string) ([]TermInfo, error) {
	r, _, err := callBuild(project, key, List{})
	if err != nil {
		return nil, err
	}
	return r.Terms, nil
}

// callBuild sends one frame to project's holder of build key, over its
// socket (SocketFor), and returns the reply and that socket.
func callBuild(project, key string, f any) (Reply, string, error) {
	sock, err := SocketFor(os.Getenv, project, key)
	if err != nil {
		return Reply{}, "", err
	}
	cl, err := Dial(sock, BuildID())
	if err != nil {
		return Reply{}, sock, fmt.Errorf("no holder answers at %s: %w", sock, err)
	}
	defer cl.Close()
	r, err := cl.Call(f)
	return r, sock, err
}

// DescribeOf is what project's holder of build key says of itself
// (Describe), asked over its socket: its unit's memory included
// (agent-resource-limits §3.1).
func DescribeOf(project, key string) (*Info, error) {
	r, sock, err := callBuild(project, key, Describe{})
	if err != nil {
		return nil, err
	}
	if r.Info == nil {
		return nil, fmt.Errorf("holder at %s described nothing", sock)
	}
	return r.Info, nil
}

// Terms counts the terminals of project's holder of build key, asked
// over its socket; an error when none answers.
func Terms(project, key string) (int, error) {
	terms, err := TermList(project, key)
	return len(terms), err
}

// AnswersAny: a holder of build key answers for some project of this
// host (its socket, either name: SocketFor): its binary copy is in use.
func AnswersAny(getenv func(string) string, key string) bool {
	root, err := RunAgentbusDir(getenv)
	if err != nil {
		return true // cannot tell: keep the copy
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return true
	}
	for _, d := range ents {
		if !d.IsDir() || !bus.ValidName(d.Name()) {
			continue
		}
		if _, err := Terms(d.Name(), key); err == nil {
			return true
		}
	}
	return false
}
