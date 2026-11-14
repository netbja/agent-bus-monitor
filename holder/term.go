package holder

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/netbja/agent-bus-monitor/screen"
)

// tailMax is the raw tail a terminal keeps of its output (spec 4.4: about
// 1 MB, for agentbus term dump).
const tailMax = 1 << 20

// terminal is one program on its own PTY. The holder always reads its
// output, feeds its emulator, and writes the emulator's answers to its
// queries back (DSR, DA: spec 4.6, S3), viewer or not.
type terminal struct {
	id, role string
	profile  string // the client's agentstate profile ("" none: never prompted)
	pid      int
	start    uint64
	cmd      *exec.Cmd
	pty      *os.File

	mu         sync.Mutex
	scr        screen.Screen
	tail       []byte
	outAt      time.Time
	alive      bool
	exit       *int
	closed     bool
	cols, rows int
	viewers    map[*viewer]bool
	last       *viewer // the most recently active viewer: its size is the terminal's
	typedAt    time.Time
	draft      bool
	draftSince time.Time

	grace   time.Duration                           // SIGHUP to SIGKILL for its group
	kill    func(pid int, sig syscall.Signal) error // nil: syscall.Kill
	others  func(pgid int) bool                     // nil: othersInGroup
	escWait time.Duration                           // a lone ESC's wait for a sequence (escWait; tests set it)
	sigMu   sync.Mutex                              // a signal to the group never crosses the leader's reaping
	ended   bool                                    // the leader was reaped: its numbers may be reused, never signalled again
	inMu    sync.Mutex                              // the input lock: every viewer byte, and Submit's critical section (step 5d)
	holdState
	leftBehind bool // a line may be left in the input: no submit until it reads empty

	done    chan struct{} // closed once the program was waited for
	outDone chan struct{} // closed once its output ended
}

// readLoop reads the program's output until the PTY ends.
func (t *terminal) readLoop() {
	defer close(t.outDone)
	buf := make([]byte, 32<<10)
	for {
		n, err := t.pty.Read(buf)
		if n > 0 {
			t.mu.Lock()
			_, _ = t.scr.Write(buf[:n])
			t.tail = append(t.tail, buf[:n]...)
			if len(t.tail) > tailMax {
				t.tail = append([]byte(nil), t.tail[len(t.tail)-tailMax:]...)
			}
			t.outAt = time.Now()
			t.fanOut(buf[:n])
			t.mu.Unlock()
			t.answer()
		}
		if err != nil {
			return
		}
	}
}

// replyEvery is how often the holder looks for the emulator's answers to
// the program's queries: the emulator produces them asynchronously, so an
// answer may be ready only after the output that asked for it was written,
// and a program waiting for it writes nothing more.
const replyEvery = 10 * time.Millisecond

// answer writes the emulator's pending answers (DSR, DA, the cursor
// position, the kitty keyboard query: spec 4.6, S3) to the program, viewer
// or not.
func (t *terminal) answer() {
	t.mu.Lock()
	replies := t.scr.Replies()
	t.mu.Unlock()
	if len(replies) > 0 {
		t.inMu.Lock() // never inside a viewer's bytes or a submit
		_, _ = t.pty.Write(replies)
		t.inMu.Unlock()
	}
}

// replyLoop answers until the output ends.
func (t *terminal) replyLoop() {
	tick := time.NewTicker(replyEvery)
	defer tick.Stop()
	for {
		select {
		case <-t.outDone:
			return
		case <-tick.C:
			t.answer()
		}
	}
}

// waitLoop waits for the program and records its exit status; -1 when a
// signal ended it.
func (t *terminal) waitLoop() {
	waitExited(t.pid) // Linux: the leader exited, still unreaped; elsewhere at once
	t.endGroup(t.grace)
	t.sigMu.Lock()
	err := t.cmd.Wait()
	t.ended = true
	t.sigMu.Unlock()
	code := 0
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		code = ee.ExitCode()
	case err != nil:
		code = -1
	}
	t.mu.Lock()
	t.alive, t.exit = false, &code
	t.mu.Unlock()
	track(t.pid, false)
	close(t.done)
}

// info is the terminal as TermInfo.
func (t *terminal) info(build string) TermInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	return TermInfo{ID: t.id, Role: t.role, PID: t.pid, PIDStart: t.start, Alive: t.alive, Exit: t.exit, Viewers: len(t.viewers), Build: build}
}

// signal sends sig to pid (negative: a process group), through the
// terminal's kill (tests record it).
func (t *terminal) signal(pid int, sig syscall.Signal) {
	kill := t.kill
	if kill == nil {
		kill = syscall.Kill
	}
	_ = kill(pid, sig)
}

// close ends the terminal (spec 4.2): SIGHUP to its process group, SIGKILL
// to it after grace. Its group is signalled only while its leader is not
// reaped, so its number cannot have been reused for another terminal
// (reviewer-codex on 5b): once the terminal ended (its group was ended at
// the leader's exit, waitLoop), a Close, a repeated one included, signals
// nothing.
func (t *terminal) close(grace time.Duration) {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	t.sigMu.Lock()
	if t.ended {
		t.sigMu.Unlock()
		_ = t.pty.Close()
		return
	}
	t.signal(-t.pid, syscall.SIGHUP)
	t.sigMu.Unlock()
	select {
	case <-t.done:
	case <-time.After(grace):
		t.sigMu.Lock()
		if !t.ended {
			t.signal(-t.pid, syscall.SIGKILL)
		}
		t.sigMu.Unlock()
		<-t.done
	}
	_ = t.pty.Close()
}

// endGroup ends what is left of the terminal's process group once its
// leader exited and before it is reaped (its pid, and so its group's, is
// not reused while it is a zombie): SIGHUP, then SIGKILL after grace. A
// child that ignores SIGHUP never outlives its terminal (reviewer-codex);
// one that left the group (setsid) is not reached.
//
// The /proc scan never decides whether the group is ended: one scan can
// miss a process forked while it reads (reviewer-codex, 5b round 3), so the
// group gets its SIGKILL whatever the scan says (the kernel's group kill
// also reaches a child forked meanwhile). The scan only spares the grace:
// a group that looks empty gets the SIGKILL at once, a busy one SIGHUP
// first, then the SIGKILL when it empties or the grace ends. Only where the
// leader is held unreaped (leaderHeld: Linux); elsewhere it was reaped
// already and its group's number may be another's.
func (t *terminal) endGroup(grace time.Duration) {
	if !leaderHeld {
		return
	}
	others := t.others
	if others == nil {
		others = othersInGroup
	}
	if others(t.pid) {
		t.signal(-t.pid, syscall.SIGHUP)
		for deadline := time.Now().Add(grace); others(t.pid) && time.Now().Before(deadline); {
			time.Sleep(20 * time.Millisecond)
		}
	}
	t.signal(-t.pid, syscall.SIGKILL)
}
