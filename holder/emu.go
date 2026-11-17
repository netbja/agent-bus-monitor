package holder

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/netbja/agent-bus-monitor/screen"
)

// The emulator's containment (holder hardening G1, spec
// 2026-11-17-holder-hardening-design.md §7.1). Each terminal's emulator runs
// in its own goroutine behind a bounded queue: the read loop never waits on
// it, so a hung or crashing emulator neither stops the program's output nor
// takes the holder (and every other agent) with it.
//
// A screen the holder cannot vouch for reads unknown, never ready:
//   - a gap (the queue was full and output was dropped) or a recovered panic
//     latches the screen "invalid";
//   - an emulator that has not advanced for stuckAfter while bytes wait
//     latches it "stuck" (that goroutine is abandoned: no second one starts);
//   - a screen read while the emulator is still behind the read loop, past
//     lagWait, is "lagging" (not latched).
//
// Nothing restores "ok" in place: a latched terminal is replaced (agentbus
// reload, resumed). The locks: t.mu is the short lock and never runs the
// emulator; t.emu is the emulator's, held by whoever calls into it.

// Screen states (TermInfo.Screen).
const (
	ScreenOK      = "ok"
	ScreenLagging = "lagging"
	ScreenInvalid = "invalid"
	ScreenStuck   = "stuck"
)

const (
	emuQueueMax = 4 << 20                // bytes waiting for the emulator before a gap
	stuckAfter  = 10 * time.Second       // no progress with bytes waiting: stuck
	lagWait     = 200 * time.Millisecond // a read waits this long for the emulator to catch up
)

// emuItem is one step for the emulator, in output order: a chunk of output
// (end: readSeq once it is parsed) or a resize.
type emuItem struct {
	data       []byte
	end        int64
	cols, rows int
}

// emuState is a terminal's containment state, under t.mu.
type emuState struct {
	emu        chan struct{} // the emulator's lock (capacity 1)
	kick       chan struct{} // capacity 1: work waits for the emulator goroutine
	queue      []emuItem
	queued     int   // bytes in queue
	readSeq    int64 // bytes read from the program
	parsedSeq  int64 // bytes the emulator has parsed
	inFlight   bool  // the emulator goroutine took an item and has not published it
	progressAt time.Time
	latched    string // "", ScreenInvalid or ScreenStuck: never cleared
	gen        int    // raised when the screen's state is latched
	stuckAfter time.Duration
	scan       queryScan // the read loop's own (read loop only)
}

func newEmuState(after time.Duration) emuState {
	if after <= 0 {
		after = stuckAfter
	}
	return emuState{emu: make(chan struct{}, 1), kick: make(chan struct{}, 1), stuckAfter: after, gen: 1}
}

// latch marks the screen invalid or stuck for good. Called with t.mu held.
func (t *terminal) latch(state, why string) {
	if t.latched != "" {
		return
	}
	t.latched, t.gen = state, t.gen+1
	t.queue, t.queued = nil, 0
	// The offset only: terminal output may hold secrets (§7.1 item 7).
	fmt.Fprintf(os.Stderr, "holder: terminal %s (%s): screen %s at byte %d: %s\n", t.id, t.role, state, t.parsedSeq, why)
}

// took records n bytes read and hands them to the emulator, never waiting
// on it: a chunk that does not fit is dropped and the screen latched invalid
// (a gap). Called with t.mu held, from the read loop.
func (t *terminal) took(chunk []byte) {
	before := t.readSeq
	t.readSeq += int64(len(chunk))
	if t.latched != "" {
		return
	}
	if t.queued+len(chunk) > emuQueueMax {
		t.latch(ScreenInvalid, fmt.Sprintf("a gap: %d bytes waiting, %d more dropped", t.queued, len(chunk)))
		return
	}
	if len(t.queue) == 0 && !t.inFlight && t.parsedSeq == before {
		t.progressAt = time.Now() // the emulator was idle: its clock starts now
	}
	t.queue = append(t.queue, emuItem{data: append([]byte(nil), chunk...), end: t.readSeq})
	t.queued += len(chunk)
	t.kickEmu()
}

// queueResize orders a resize after the output already read. Called with
// t.mu held.
func (t *terminal) queueResize(cols, rows int) {
	if t.latched != "" {
		return
	}
	if len(t.queue) == 0 && !t.inFlight {
		t.progressAt = time.Now()
	}
	t.queue = append(t.queue, emuItem{cols: cols, rows: rows})
	t.kickEmu()
}

func (t *terminal) kickEmu() {
	select {
	case t.kick <- struct{}{}:
	default:
	}
}

// emuLoop is the terminal's emulator goroutine: one item at a time, the
// emulator's lock held while it runs, the progress published under the
// short lock before that lock is released (§7.1 item 5). It ends with the
// output, or once the screen is latched (a stuck one never comes back to
// look: it is abandoned).
func (t *terminal) emuLoop() {
	for {
		select {
		case <-t.kick:
		case <-t.outDone:
			t.mu.Lock()
			empty := len(t.queue) == 0
			t.mu.Unlock()
			if empty {
				return
			}
		}
		for {
			t.mu.Lock()
			if t.latched != "" || len(t.queue) == 0 {
				latched := t.latched != ""
				t.mu.Unlock()
				if latched {
					return
				}
				break
			}
			it := t.queue[0]
			t.queue = t.queue[1:]
			t.queued -= len(it.data)
			t.inFlight = true
			t.mu.Unlock()

			t.emu <- struct{}{}
			err := t.run(func(s screen.Screen) {
				if it.data != nil {
					_, _ = s.Write(it.data)
				} else {
					s.Resize(it.cols, it.rows)
				}
			})
			t.mu.Lock()
			t.inFlight = false
			if err != nil {
				t.latch(ScreenInvalid, err.Error())
			} else if it.data != nil {
				t.parsedSeq, t.progressAt = it.end, time.Now()
			}
			t.mu.Unlock()
			<-t.emu
			t.answer()
		}
	}
}

// run calls the emulator with its lock held by the caller; a panic is
// recovered and returned, so the lock is released (§7.1 item 7).
func (t *terminal) run(f func(screen.Screen)) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the emulator panicked: %v", r)
		}
	}()
	f(t.scr)
	return nil
}

// lockEmu takes the emulator's lock, waiting at most d; false when it could
// not (the emulator is busy, or stuck).
func (t *terminal) lockEmu(d time.Duration) bool {
	select {
	case t.emu <- struct{}{}:
		return true
	default:
	}
	if d <= 0 {
		return false
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case t.emu <- struct{}{}:
		return true
	case <-timer.C:
		return false
	}
}

// watch latches a stuck emulator: bytes wait and it has not advanced for
// stuckAfter. Called with t.mu held (the reply loop's tick).
func (t *terminal) watch(now time.Time) {
	waiting := len(t.queue) > 0 || t.inFlight
	if t.latched == "" && waiting && !t.progressAt.IsZero() && now.Sub(t.progressAt) >= t.stuckAfter {
		t.latch(ScreenStuck, fmt.Sprintf("no progress for %s with %d bytes waiting", t.stuckAfter, t.readSeq-t.parsedSeq))
	}
}

// screenState is the state info reports, never waiting: a latched state, or
// lagging while the emulator is behind. Called with t.mu held.
func (t *terminal) screenState() string {
	switch {
	case t.latched != "":
		return t.latched
	case len(t.queue) > 0 || t.inFlight || t.parsedSeq != t.readSeq:
		return ScreenLagging
	}
	return ScreenOK
}

// view calls read on the emulator only when the screen is caught up: the
// emulator's lock held, and at that instant everything read has been
// parsed (§7.1 item 5). It waits up to wait for that; the state it returns
// is ScreenOK when read ran, else why not (lagging, invalid, stuck).
func (t *terminal) view(wait time.Duration, read func(screen.Screen)) string {
	deadline := time.Now().Add(wait)
	for {
		t.mu.Lock()
		latched := t.latched
		t.mu.Unlock()
		if latched != "" {
			return latched
		}
		if !t.lockEmu(time.Until(deadline)) {
			t.mu.Lock()
			st := t.screenState()
			t.mu.Unlock()
			if st == ScreenOK { // free a moment ago, taken again since: still behind
				st = ScreenLagging
			}
			return st
		}
		t.mu.Lock()
		st := t.screenState()
		t.mu.Unlock()
		if st == ScreenOK {
			err := t.run(read)
			if err != nil {
				t.mu.Lock()
				t.latch(ScreenInvalid, err.Error())
				t.mu.Unlock()
				st = ScreenInvalid
			}
			<-t.emu
			return st
		}
		<-t.emu
		if st != ScreenLagging || !time.Now().Before(deadline) {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The static answers a terminal gives whatever its screen (§7.1 item 6): the
// emulator's own (x/vt), so a client waiting on one at start or on a redraw
// is answered while the screen is not trusted. Never a cursor position.
var (
	answerDA1 = ansi.PrimaryDeviceAttributes(62, 1, 6, 22)
	answerDA2 = ansi.SecondaryDeviceAttributes(1, 10, 0)
	answerDSR = ansi.DeviceStatusReport(ansi.DECStatusReport(0))
)

// queryScan finds the static queries in the output stream, split across
// chunks or not.
type queryScan struct{ carry []byte }

// queryMax is the longest query scanned, minus one: the carry kept.
const queryMax = len("\x1b[>0c") - 1

// answers is what the queries in chunk ask for, in order.
func (q *queryScan) answers(chunk []byte) []byte {
	buf := append(q.carry, chunk...)
	var out []byte
	for i := 0; i < len(buf); i++ {
		if buf[i] != 0x1b {
			continue
		}
		rest := buf[i:]
		switch {
		case bytes.HasPrefix(rest, []byte("\x1b[c")), bytes.HasPrefix(rest, []byte("\x1b[0c")):
			out = append(out, answerDA1...)
		case bytes.HasPrefix(rest, []byte("\x1b[>c")), bytes.HasPrefix(rest, []byte("\x1b[>0c")):
			out = append(out, answerDA2...)
		case bytes.HasPrefix(rest, []byte("\x1b[5n")):
			out = append(out, answerDSR...)
		}
	}
	// The carry: a tail that begins a query still arriving (a strict
	// prefix of one), never a complete one, already answered.
	q.carry = nil
	tail := buf[len(buf)-min(queryMax, len(buf)):]
	if j := bytes.LastIndexByte(tail, 0x1b); j >= 0 && queryPrefix(tail[j:]) {
		q.carry = append([]byte(nil), tail[j:]...)
	}
	return out
}

var queries = [][]byte{[]byte("\x1b[c"), []byte("\x1b[0c"), []byte("\x1b[>c"), []byte("\x1b[>0c"), []byte("\x1b[5n")}

// queryPrefix: b is a strict prefix of a query.
func queryPrefix(b []byte) bool {
	for _, q := range queries {
		if len(b) < len(q) && bytes.HasPrefix(q, b) {
			return true
		}
	}
	return false
}
