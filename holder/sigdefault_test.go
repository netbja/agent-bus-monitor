package holder

import (
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// A holder started with SIGHUP ignored (nohup, a runner that ignores it)
// still hangs up its terminals: an ignored signal is inherited through exec,
// so without the holder taking it back its programs would ignore Close's
// SIGHUP and end only at the grace's SIGKILL (holder-closehangsup-flake:
// TestHolderCloseHangsUpFirst took 2.06 s under such a runner).
func TestHolderHangsUpWhenStartedWithSIGHUPIgnored(t *testing.T) {
	// No signal.Reset after: it would put back the disposition the process
	// started with (ignored, under nohup), and signal.Ignored no longer
	// reports one restored that way. The holder's handler stays (it drops
	// SIGHUP), as it does in the holder process itself.
	signal.Ignore(syscall.SIGHUP)
	_, sock := newTestHolder(t) // grace 2 s
	cl := dial(t, sock)
	x := startRole(t, cl, "echoer")
	waitScreen(t, cl, x.ID, "hello")
	began := time.Now()
	if _, err := cl.Call(Close{Term: x.ID}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(began); d > time.Second {
		t.Fatalf("close took %v: the program inherited the ignored SIGHUP", d)
	}
}

// unignoreTermSignals takes back an ignored terminal signal (SIGHUP here:
// SIGQUIT is go test's way to a goroutine dump, never dropped in a test).
func TestUnignoreTermSignals(t *testing.T) {
	signal.Ignore(syscall.SIGHUP)
	unignoreTermSignals()
	if signal.Ignored(syscall.SIGHUP) {
		t.Fatal("SIGHUP still ignored")
	}
}
