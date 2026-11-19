package holder

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/service"
)

// Ready is up's readiness probe: the holder's handshake, bounded, not a bare
// dial (a socket listening before its accept loop serves nothing yet;
// reviewer-codex). A listener that never accepts, or accepts and says
// nothing, is not ready, within the timeout; a real holder is. Moved from
// cmd/agentbus with holderReady (dispatchsvc P3), its serving case on a holder
// of this package instead of an installed agentbus.
func TestHolderReadyProbesTheProtocol(t *testing.T) {
	_, getenv := shortRundir(t)
	key := service.BuildKey("v1")
	sock, err := SocketPath(getenv, "demo", key)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if Ready(getenv, "demo", key, 300*time.Millisecond) {
		t.Fatal("a listener that never accepts read as ready")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the probe took %v", d)
	}
	go func() { // accepts and says nothing
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	if Ready(getenv, "demo", key, 300*time.Millisecond) {
		t.Fatal("a silent listener read as ready")
	}
	ln.Close()

	rundir, wenv := shortRundir(t)
	serveIn(t, rundir, BuildID(), testRoles(t))
	if !Ready(wenv, "demo", service.BuildKey(BuildID()), 2*time.Second) {
		t.Fatal("a serving holder read as not ready")
	}
}

// TermList and Terms ask a project's holder of a build over its socket (the
// process environment's run directory); none answering is an error that
// names the socket.
func TestHolderTermsAskTheSocket(t *testing.T) {
	rundir, _ := shortRundir(t)
	t.Setenv("XDG_RUNTIME_DIR", rundir)
	key := service.BuildKey(BuildID())
	if _, err := TermList("demo", key); err == nil || !strings.Contains(err.Error(), "no holder answers at ") {
		t.Fatalf("no holder: %v", err)
	}
	if n, err := Terms("demo", key); err == nil || n != 0 {
		t.Fatalf("no holder: %d, %v", n, err)
	}
	serveIn(t, rundir, BuildID(), testRoles(t))
	cl := dial(t, mustSocket(t, rundir, key))
	startRole(t, cl, "echoer")
	terms, err := TermList("demo", key)
	if err != nil || len(terms) != 1 || terms[0].Role != "echoer" {
		t.Fatalf("TermList = %+v, %v", terms, err)
	}
	if n, err := Terms("demo", key); err != nil || n != 1 {
		t.Fatalf("Terms = %d, %v", n, err)
	}
}

// AnswersAny: a holder of the build answers for some project of this host,
// so its binary copy is in use; a run directory that cannot be read keeps
// the copy too.
func TestHolderAnswersAny(t *testing.T) {
	rundir, getenv := shortRundir(t)
	t.Setenv("XDG_RUNTIME_DIR", rundir)
	key := service.BuildKey(BuildID())
	// A run directory that cannot be made (XDG_RUNTIME_DIR a file): cannot tell.
	file := filepath.Join(rundir, "a-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !AnswersAny(func(k string) string { return map[string]string{"XDG_RUNTIME_DIR": file}[k] }, key) {
		t.Error("an unreadable run directory: not kept")
	}
	root, err := RunAgentbusDir(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "other"), 0o700); err != nil {
		t.Fatal(err)
	}
	if AnswersAny(getenv, key) {
		t.Error("no holder answers: kept")
	}
	serveIn(t, rundir, BuildID(), testRoles(t))
	if !AnswersAny(getenv, key) {
		t.Error("demo's holder answers: not kept")
	}
	if AnswersAny(getenv, service.BuildKey("another")) {
		t.Error("another build: kept")
	}
}

func mustSocket(t *testing.T, rundir, key string) string {
	t.Helper()
	sock, err := SocketFor(func(k string) string {
		if k == "XDG_RUNTIME_DIR" {
			return rundir
		}
		return ""
	}, "demo", key)
	if err != nil {
		t.Fatal(err)
	}
	return sock
}
