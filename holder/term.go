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
	pid      int
	start    uint64
	cmd      *exec.Cmd
	pty      *os.File

	mu     sync.Mutex
	scr    screen.Screen
	tail   []byte
	outAt  time.Time
	alive  bool
	exit   *int
	closed bool

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
		_, _ = t.pty.Write(replies)
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
	err := t.cmd.Wait()
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
	return TermInfo{ID: t.id, Role: t.role, PID: t.pid, PIDStart: t.start, Alive: t.alive, Exit: t.exit, Build: build}
}

// close ends the program: SIGHUP to its process group, then SIGKILL after
// grace (spec 4.2). It returns once the program was waited for.
func (t *terminal) close(grace time.Duration) {
	t.mu.Lock()
	alive := t.alive
	t.closed = true
	t.mu.Unlock()
	if alive {
		_ = syscall.Kill(-t.pid, syscall.SIGHUP)
		select {
		case <-t.done:
		case <-time.After(grace):
			_ = syscall.Kill(-t.pid, syscall.SIGKILL)
			<-t.done
		}
	}
	_ = t.pty.Close()
}
