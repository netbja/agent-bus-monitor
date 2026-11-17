package holder

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/screen"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// faultScreen is the real emulator with injected faults (holder hardening
// G1 §7.1's fault-injection tests): a pause, a delay per write, a hang or a
// panic on a marker, a panic on a snapshot.
type faultScreen struct {
	screen.Screen
	mu        sync.Mutex
	gate      chan struct{} // non-nil: each Write waits until it is closed
	delay     time.Duration
	hangOn    string
	panicOn   string
	snapPanic bool
	// replies, when set, answers each Replies call in turn (nil: panic);
	// past its end, the emulator's own.
	replies       [][]byte
	beforeReplies func() // runs in a scripted Replies call, before its answer
}

func (f *faultScreen) Replies() []byte {
	f.mu.Lock()
	if len(f.replies) == 0 {
		f.mu.Unlock()
		return f.Screen.Replies()
	}
	r := f.replies[0]
	f.replies = f.replies[1:]
	f.mu.Unlock()
	f.mu.Lock()
	before := f.beforeReplies
	f.mu.Unlock()
	if before != nil {
		before()
	}
	if r == nil {
		panic("injected replies panic")
	}
	return r
}

func (f *faultScreen) Write(p []byte) (int, error) {
	f.mu.Lock()
	gate, delay, hang, boom := f.gate, f.delay, f.hangOn, f.panicOn
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	time.Sleep(delay)
	if hang != "" && bytes.Contains(p, []byte(hang)) {
		select {} // a parser that never returns
	}
	if boom != "" && bytes.Contains(p, []byte(boom)) {
		panic("injected parser panic")
	}
	return f.Screen.Write(p)
}

func (f *faultScreen) Snapshot(lines int) screen.Snapshot {
	f.mu.Lock()
	boom := f.snapPanic
	f.mu.Unlock()
	if boom {
		panic("injected snapshot panic")
	}
	return f.Screen.Snapshot(lines)
}

func (f *faultScreen) set(fn func(*faultScreen)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// faultHolder serves a holder whose terminals run scripts[role] (else the
// usual test roles), each on its own faultScreen, kept by role.
type faultHolder struct {
	sock    string
	mu      sync.Mutex
	screens map[string]*faultScreen // by the order started: "1", "2"…
	n       int
}

func newFaultHolder(t *testing.T, scripts map[string]string, set func(*Config)) *faultHolder {
	t.Helper()
	fh := &faultHolder{screens: map[string]*faultScreen{}}
	roles := testRoles(t)
	_, fh.sock = newTestHolderWith(t, func(c *Config) {
		c.Launch = func(s Start) (Command, error) {
			if sc, ok := scripts[s.Role]; ok {
				return Command{Argv: []string{"/bin/sh", "-c", sc}}, nil
			}
			return roles(s)
		}
		c.newScreen = func(cols, rows, sb int) screen.Screen {
			f := &faultScreen{Screen: screen.New(cols, rows, sb)}
			fh.mu.Lock()
			fh.n++
			fh.screens["t"+strconv.Itoa(fh.n)] = f
			fh.mu.Unlock()
			return f
		}
		if set != nil {
			set(c)
		}
	})
	return fh
}

func (fh *faultHolder) screen(id string) *faultScreen {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	return fh.screens[id]
}

// stateOf is the terminal's screen state as List reports it.
func stateOf(t *testing.T, cl *Client, id string) (string, int) {
	t.Helper()
	ti := termOf(t, cl, id)
	return ti.Screen, ti.ScreenGen
}

func waitState(t *testing.T, cl *Client, id, want string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		st, _ := stateOf(t, cl, id)
		if st == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s's screen is %q, never %q", id, st, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// screenErr is the Screen request's refusal code and text ("ok" when it
// gave a screen).
func screenErr(cl *Client, id string) (string, string) {
	_, err := cl.Call(ScreenReq{Term: id})
	if err == nil {
		return "ok", ""
	}
	return codeOf(err), err.Error()
}

// The queue overflows while the parser pauses: the screen is invalid from
// the gap, the program never blocked (it wrote past the flood), and the
// screen stays invalid after the parser resumes and the program clears the
// screen, switches to the alternate screen and redraws an input box after
// a resize. Only a replacing terminal reads ok again.
func TestEmuGapLatchesInvalid(t *testing.T) {
	dir := t.TempDir()
	fh := newFaultHolder(t, map[string]string{
		"flood": `printf 'ready\n'; sleep 0.4; head -c 6000000 /dev/zero | tr '\0' x; echo done > ` + dir + `/done; ` +
			`sleep 0.4; printf '\033[2J\033[H> \033[?1049h\033[2J\033[H> '; exec sleep 30`,
	}, nil)
	cl := dial(t, fh.sock)
	a := startRole(t, cl, "flood")
	waitScreen(t, cl, a.ID, "ready")
	f := fh.screen(a.ID)
	gate := make(chan struct{})
	f.set(func(f *faultScreen) { f.gate = gate })
	waitState(t, cl, a.ID, ScreenInvalid, 10*time.Second)
	if _, gen := stateOf(t, cl, a.ID); gen < 2 {
		t.Errorf("generation %d: not raised by the latch", gen)
	}
	if !until(10*time.Second, func() bool { _, err := os.Stat(filepath.Join(dir, "done")); return err == nil }) {
		t.Fatal("the program blocked on the paused parser")
	}
	f.set(func(f *faultScreen) { f.gate = nil })
	close(gate)
	v := attached(t, fh.sock, Attach{Term: a.ID, Cols: 90, Rows: 20}) // a resize after the gap
	_ = v
	time.Sleep(time.Second) // the clear, the alternate screen and the box drawn
	if code, msg := screenErr(cl, a.ID); code != CodeScreen || !strings.Contains(msg, ScreenInvalid) {
		t.Fatalf("after the redraw: %s %s", code, msg)
	}
	if st, _ := stateOf(t, cl, a.ID); st != ScreenInvalid {
		t.Fatalf("after the redraw: %s", st)
	}
	b := startRole(t, cl, "echoer") // the replacing terminal
	waitScreen(t, cl, b.ID, "hello")
	if st, _ := stateOf(t, cl, b.ID); st != ScreenOK {
		t.Fatalf("a new terminal reads %s", st)
	}
}

// A parser panic is recovered: that terminal's screen is invalid, latched,
// and every other terminal (and the holder) keeps running; a panic in a
// snapshot is recovered the same way.
func TestEmuPanicLatchesInvalid(t *testing.T) {
	fh := newFaultHolder(t, map[string]string{
		"boomer": `printf 'ready\n'; sleep 0.4; printf 'BOOM'; sleep 0.4; printf 'after\n'; exec sleep 30`,
	}, nil)
	cl := dial(t, fh.sock)
	a := startRole(t, cl, "boomer")
	b := startRole(t, cl, "echoer")
	waitScreen(t, cl, a.ID, "ready")
	fh.screen(a.ID).set(func(f *faultScreen) { f.panicOn = "BOOM" })
	waitState(t, cl, a.ID, ScreenInvalid, 5*time.Second)
	time.Sleep(600 * time.Millisecond) // more output after the panic
	if code, _ := screenErr(cl, a.ID); code != CodeScreen {
		t.Fatalf("boomer's screen: %s", code)
	}
	if st, _ := stateOf(t, cl, a.ID); st != ScreenInvalid {
		t.Fatalf("boomer after more output: %s", st)
	}
	waitScreen(t, cl, b.ID, "hello")
	fh.screen(b.ID).set(func(f *faultScreen) { f.snapPanic = true })
	if code, _ := screenErr(cl, b.ID); code != CodeScreen {
		t.Fatalf("a snapshot panic: %s", code)
	}
	if st, _ := stateOf(t, cl, b.ID); st != ScreenInvalid {
		t.Fatalf("after a snapshot panic: %s", st)
	}
}

// Lag: the parser 600 ms behind what was read while its last screen looks
// ready: Screen answers lagging (not latched), never that screen; once it
// caught up, ok.
func TestEmuLaggingIsNotAScreen(t *testing.T) {
	fh := newFaultHolder(t, map[string]string{
		"slow": `printf 'ready\n'; sleep 0.6; printf 'more\n'; exec sleep 30`,
	}, nil)
	cl := dial(t, fh.sock)
	a := startRole(t, cl, "slow")
	waitScreen(t, cl, a.ID, "ready")
	fh.screen(a.ID).set(func(f *faultScreen) { f.delay = 800 * time.Millisecond })
	if !until(3*time.Second, func() bool {
		code, msg := screenErr(cl, a.ID)
		return code == CodeScreen && strings.Contains(msg, ScreenLagging)
	}) {
		t.Fatal("a parser behind the read loop never read lagging")
	}
	waitScreen(t, cl, a.ID, "more") // caught up: ok again, not latched
	if st, _ := stateOf(t, cl, a.ID); st != ScreenOK {
		t.Fatalf("caught up: %s", st)
	}
}

// One terminal's parser hung: every other terminal's Screen, Activity,
// attach and List answer at once; the hung one reports stuck. While stuck,
// a DA and a DSR 5 are answered by the read loop's scanner, a cursor
// position query is not; the program keeps running.
func TestEmuStuckIsContained(t *testing.T) {
	dir := t.TempDir()
	fh := newFaultHolder(t, map[string]string{
		"hanger": `stty raw -echo; printf 'ready\n'; sleep 0.4; printf 'HANG'; sleep 1; ` +
			`printf '\033[c'; dd bs=1 count=` + strconv.Itoa(len(answerDA1)) + ` 2>/dev/null > ` + dir + `/da; ` +
			`printf '\033[5n'; dd bs=1 count=` + strconv.Itoa(len(answerDSR)) + ` 2>/dev/null > ` + dir + `/dsr; ` +
			`printf '\033[6n'; timeout --foreground 1 dd bs=1 count=1 2>/dev/null > ` + dir + `/cpr; ` +
			`echo end > ` + dir + `/end; exec sleep 30`,
	}, func(c *Config) { c.stuckAfter = 300 * time.Millisecond })
	cl := dial(t, fh.sock)
	a := startRole(t, cl, "hanger")
	b := startRole(t, cl, "echoer")
	waitScreen(t, cl, a.ID, "ready")
	waitScreen(t, cl, b.ID, "hello")
	fh.screen(a.ID).set(func(f *faultScreen) { f.hangOn = "HANG" })
	waitState(t, cl, a.ID, ScreenStuck, 5*time.Second)
	start := time.Now()
	if code, _ := screenErr(cl, b.ID); code != "ok" {
		t.Fatalf("the other terminal's screen: %s", code)
	}
	if _, err := cl.Call(ActivityReq{Term: b.ID}); err != nil {
		t.Fatal(err)
	}
	r, err := cl.Call(ActivityReq{Term: a.ID})
	if err != nil || r.Activity.Screen != ScreenStuck {
		t.Fatalf("the hung one's activity: %+v %v", r.Activity, err)
	}
	if code, msg := screenErr(cl, a.ID); code != CodeScreen || !strings.Contains(msg, ScreenStuck) {
		t.Fatalf("the hung one's screen: %s %s", code, msg)
	}
	attached(t, fh.sock, Attach{Term: b.ID, Cols: 80, Rows: 20, Observe: true})
	attached(t, fh.sock, Attach{Term: a.ID, Cols: 80, Rows: 20, Observe: true})
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the others waited on the hung parser: %s", d)
	}
	if !until(10*time.Second, func() bool { _, err := os.Stat(filepath.Join(dir, "end")); return err == nil }) {
		t.Fatal("the program froze")
	}
	read := func(n string) string { b, _ := os.ReadFile(filepath.Join(dir, n)); return string(b) }
	if read("da") != answerDA1 || read("dsr") != answerDSR || read("cpr") != "" {
		t.Fatalf("answers while stuck: da %q dsr %q cpr %q", read("da"), read("dsr"), read("cpr"))
	}
}

// Across the states: nothing is submitted into a terminal whose screen is
// not ok.
func TestEmuNotOKIsNeverSubmitted(t *testing.T) {
	fh := newFaultHolder(t, nil, nil)
	cl := dial(t, fh.sock)
	a := startRole(t, cl, "fake")
	waitScreen(t, cl, a.ID, "")
	if !until(5*time.Second, func() bool {
		_, err := cl.Call(Submit{Kind: "wake", Term: a.ID, Nonce: "0a0b0c", Count: 1})
		return err == nil
	}) {
		t.Fatal("the fake never took a wake on an ok screen")
	}
	fh.screen(a.ID).set(func(f *faultScreen) { f.snapPanic = true })
	_, err := cl.Call(Submit{Kind: "wake", Term: a.ID, Nonce: "0d0e0f", Count: 1})
	if code := codeOf(err); code != CodeNotReady {
		t.Fatalf("a submit on an invalid screen: %s", code)
	}
	if st, _ := stateOf(t, cl, a.ID); st != ScreenInvalid {
		t.Fatalf("state %s", st)
	}
}

// The scanner finds a query split across reads, and answers a complete one
// once only.
func TestQueryScanAcrossChunks(t *testing.T) {
	var q queryScan
	if got := string(q.answers([]byte("abc\x1b["))) + string(q.answers([]byte(">0cdef\x1b[5n"))); got != answerDA2+answerDSR {
		t.Fatalf("split: %q", got)
	}
	if got := q.answers([]byte("x")); len(got) != 0 {
		t.Fatalf("answered twice: %q", got)
	}
	if got := string(q.answers([]byte("\x1b[c\x1b[6n"))); got != answerDA1 {
		t.Fatalf("a cursor query answered: %q", got)
	}
}

func until(d time.Duration, cond func() bool) bool {
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

// reviewer-codex: output the read loop takes while view reads the emulator
// is not in that snapshot, so view never calls it ok: it discards the read
// and tries again, and with the emulator still behind answers lagging.
func TestViewDiscardsAReadOvertakenByOutput(t *testing.T) {
	term := &terminal{scr: screen.New(20, 5, 10), emuState: newEmuState(0)}
	reads := 0
	term.afterViewCheck = func() {
		if reads == 0 {
			term.mu.Lock()
			term.took([]byte("> ready")) // the read loop, between the check and the read
			term.mu.Unlock()
		}
	}
	st := term.view(50*time.Millisecond, func(screen.Screen) { reads++ })
	if st != ScreenLagging || reads != 1 {
		t.Fatalf("state %s after %d read(s): an overtaken read was called ok", st, reads)
	}
	go term.emuLoop() // the emulator catches up: the next read holds
	t.Cleanup(func() { term.mu.Lock(); term.latched = ScreenInvalid; term.mu.Unlock(); term.kickEmu() })
	if st := term.view(time.Second, func(screen.Screen) { reads++ }); st != ScreenOK || reads != 2 {
		t.Fatalf("caught up: %s after %d read(s)", st, reads)
	}
}

// queryProbe is a program that asks a DA right after mark, reads exactly
// its answer into dir/da, then waits 0.5 s for any second answer
// (dir/extra), and ends (dir/end).
func queryProbe(dir, before string) string {
	return `stty raw -echo; printf 'ready\n'; sleep 0.4; ` + before +
		`printf '\033[c'; dd bs=1 count=` + strconv.Itoa(len(answerDA1)) + ` 2>/dev/null > ` + dir + `/da; ` +
		`timeout --foreground 0.5 dd bs=1 count=1 2>/dev/null > ` + dir + `/extra; echo end > ` + dir + `/end; exec sleep 30`
}

// answeredOnce: the probe ended with the DA answered exactly once.
func answeredOnce(t *testing.T, dir string) {
	t.Helper()
	if !until(10*time.Second, func() bool { _, err := os.Stat(filepath.Join(dir, "end")); return err == nil }) {
		t.Fatal("the program froze waiting for its DA")
	}
	read := func(n string) string { b, _ := os.ReadFile(filepath.Join(dir, n)); return string(b) }
	if read("da") != answerDA1 || read("extra") != "" {
		t.Fatalf("da %q, then %q", read("da"), read("extra"))
	}
}

// foureyes: a DA sent after the emulator hung, before the stuck latch, is
// answered once the latch comes (its static answer was pending), once.
func TestEmuQueryInTheHangWindowIsAnswered(t *testing.T) {
	dir := t.TempDir()
	fh := newFaultHolder(t, map[string]string{"prober": queryProbe(dir, `printf 'HANG'; sleep 0.05; `)},
		func(c *Config) { c.stuckAfter = 300 * time.Millisecond })
	cl := dial(t, fh.sock)
	a := startRole(t, cl, "prober")
	waitScreen(t, cl, a.ID, "ready")
	fh.screen(a.ID).set(func(f *faultScreen) { f.hangOn = "HANG" })
	answeredOnce(t, dir)
	if st, _ := stateOf(t, cl, a.ID); st != ScreenStuck {
		t.Fatalf("state %s", st)
	}
}

// foureyes: a DA queued for a paused emulator, then dropped with the queue
// by a gap's latch, is answered at the latch, once.
func TestEmuQueryInAGapIsAnswered(t *testing.T) {
	dir := t.TempDir()
	fh := newFaultHolder(t, map[string]string{"prober": queryProbe(dir, `printf '\033[c'; head -c 6000000 /dev/zero | tr '\0' x; `)}, nil)
	// The probe asks twice: the first DA before the flood (dropped by the
	// gap), read by its dd after the flood; the second is answered at once.
	cl := dial(t, fh.sock)
	a := startRole(t, cl, "prober")
	waitScreen(t, cl, a.ID, "ready")
	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	fh.screen(a.ID).set(func(f *faultScreen) { f.gate = gate })
	if !until(10*time.Second, func() bool { _, err := os.Stat(filepath.Join(dir, "end")); return err == nil }) {
		t.Fatal("the program froze waiting for its DA")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "da"))
	extra, _ := os.ReadFile(filepath.Join(dir, "extra"))
	// Two queries, two answers: the dd reads the first, the second waits in
	// the input (extra reads its first byte).
	if string(b) != answerDA1 || string(extra) != answerDA1[:1] {
		t.Fatalf("da %q, then %q", b, extra)
	}
	if st, _ := stateOf(t, cl, a.ID); st != ScreenInvalid {
		t.Fatalf("state %s", st)
	}
}

// foureyes: a trusted emulator answers a DA itself; the scanner adds no
// second answer.
func TestEmuHealthyAnswersOnce(t *testing.T) {
	dir := t.TempDir()
	fh := newFaultHolder(t, map[string]string{"prober": queryProbe(dir, "")}, nil)
	cl := dial(t, fh.sock)
	a := startRole(t, cl, "prober")
	answeredOnce(t, dir)
	if st, _ := stateOf(t, cl, a.ID); st != ScreenOK {
		t.Fatalf("state %s", st)
	}
}

// foureyes: output read while a viewer's paint renders reaches it after
// the paint, never before.
func TestPaintKeepsOutputBehindIt(t *testing.T) {
	v := &viewer{out: make(chan []byte, 8), repaint: true}
	term := &terminal{viewers: map[*viewer]bool{v: true}, scr: screen.New(20, 5, 10), emuState: newEmuState(0)}
	term.afterPaintRegister = func() {
		term.mu.Lock()
		term.fanOut([]byte("after"))
		term.mu.Unlock()
	}
	term.paint(v)
	if len(v.out) != 2 {
		t.Fatalf("%d chunks", len(v.out))
	}
	if first, second := string(<-v.out), string(<-v.out); !strings.Contains(first, "\x1b[") || second != "after" {
		t.Fatalf("order %q, %q", first, second)
	}
}

// foureyes: a viewer's resize reaches the emulator, in order with the
// output.
func TestResizeReachesTheEmulator(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	term := &terminal{pty: w, scr: screen.New(20, 5, 10), cols: 20, rows: 5, emuState: newEmuState(0)}
	go term.emuLoop()
	t.Cleanup(func() { term.mu.Lock(); term.latched = ScreenInvalid; term.mu.Unlock(); term.kickEmu() })
	term.mu.Lock()
	term.resize(40, 5)
	term.took([]byte(strings.Repeat("x", 25)))
	term.mu.Unlock()
	var snap screen.Snapshot
	if st := term.view(time.Second, func(s screen.Screen) { snap = s.Snapshot(0) }); st != ScreenOK {
		t.Fatalf("state %s", st)
	}
	if snap.Cols != 40 || !strings.Contains(snap.Plain(), strings.Repeat("x", 25)) {
		t.Fatalf("cols %d:\n%s", snap.Cols, snap.Plain())
	}
}

// foureyes: the holder's code screen reaches the dispatcher as
// termhost.ErrScreenUntrusted.
func TestMapErrScreen(t *testing.T) {
	if err := mapErr(&RemoteError{Code: CodeScreen, Msg: "terminal t1's screen is stuck"}); !errors.Is(err, termhost.ErrScreenUntrusted) {
		t.Fatalf("%v", err)
	}
}

// reviewer-codex: a DA parsed, an answer pass that sees no reply yet, then
// a Replies panic: the latch pays the pending DA, once. A pending answer is
// retired only by its reply observed, never by its parse.
func TestRepliesPanicPaysThePendingDA(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	f := &faultScreen{Screen: screen.New(20, 5, 10), replies: [][]byte{{}, nil}}
	term := &terminal{pty: w, scr: f, emuState: newEmuState(0)}
	term.mu.Lock()
	term.owe(term.scan.find([]byte("\x1b[c")))
	term.readSeq, term.parsedSeq = 3, 3 // the emulator parsed it
	term.mu.Unlock()
	term.answer() // no reply yet: still pending
	term.answer() // Replies panics: the latch owes it
	term.payOwed()
	term.payOwed()
	w.Close()
	got, _ := io.ReadAll(r)
	if string(got) != answerDA1 {
		t.Fatalf("written %q, want the DA answer once", got)
	}
	if st, _ := stateOf2(term); st != ScreenInvalid {
		t.Fatalf("state %s", st)
	}
}

// A reply observed retires its pending answer, in order, whatever lies
// between (a cursor report), so a later latch pays nothing twice.
func TestAnsweredRetiresByObservation(t *testing.T) {
	term := &terminal{emuState: newEmuState(0)}
	term.owe([][]byte{[]byte(answerDA1), []byte(answerDSR), []byte(answerDA1)})
	term.answered([]byte(answerDA1 + "\x1b[3;1R" + answerDSR))
	if len(term.pending) != 1 || string(term.pending[0].b) != answerDA1 {
		t.Fatalf("pending %q", term.pending)
	}
	term.latch(ScreenStuck, "test")
	if string(term.owed) != answerDA1 {
		t.Fatalf("owed %q", term.owed)
	}
}

func stateOf2(t *terminal) (string, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.screenState(), t.gen
}

// reviewer-codex: a latch between an answer pass's Replies and its short
// lock (a gap in the read loop) never pays a static answer the pass
// collected from the emulator: one answer per query, the emulator's.
func TestLatchDuringAnAnswerPassAnswersOnce(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	f := &faultScreen{Screen: screen.New(20, 5, 10), replies: [][]byte{[]byte(answerDA1)}}
	term := &terminal{pty: w, scr: f, emuState: newEmuState(0)}
	term.mu.Lock()
	term.owe(term.scan.find([]byte("\x1b[c\x1b[5n"))) // a DA the emulator answers, a DSR it has not yet
	term.mu.Unlock()
	term.afterReplies = func() {
		term.mu.Lock()
		term.latch(ScreenInvalid, "a gap") // the read loop, in the window
		term.mu.Unlock()
		term.payOwed()
	}
	term.answer()
	term.payOwed()
	w.Close()
	got, _ := io.ReadAll(r)
	if string(got) != answerDA1+answerDSR {
		t.Fatalf("written %q, want the emulator's DA and the owed DSR, once each", got)
	}
}

// A pass that hangs collecting replies on a latched screen is abandoned
// by the watch: its pending answers are owed, and it writes nothing if it
// returns.
func TestWatchAbandonsAHungAnswerPass(t *testing.T) {
	term := &terminal{emuState: newEmuState(time.Second)}
	term.owe([][]byte{[]byte(answerDA1)})
	term.answering, term.answeringAt = true, time.Now().Add(-2*time.Second)
	term.latch(ScreenStuck, "test")
	if len(term.owed) != 0 {
		t.Fatal("the latch owed answers the pass still holds")
	}
	term.watch(time.Now())
	if string(term.owed) != answerDA1 || term.answering {
		t.Fatalf("owed %q, answering %v", term.owed, term.answering)
	}
}

// pipeTerm is a hand-built terminal on f whose writes to the program are
// read back from the pipe once it is closed.
func pipeTerm(t *testing.T, f *faultScreen) (*terminal, func() string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	term := &terminal{pty: w, scr: f, emuState: newEmuState(0)}
	return term, func() string { w.Close(); b, _ := io.ReadAll(r); return string(b) }
}

// reviewer-codex (1): a latch between an answer pass's first check and its
// mark pays the static DA; the pass then rechecks under the short lock and
// writes nothing of the emulator's: one answer.
func TestLatchBeforeTheAnswerPassMarksAnswersOnce(t *testing.T) {
	term, written := pipeTerm(t, &faultScreen{Screen: screen.New(20, 5, 10), replies: [][]byte{[]byte(answerDA1)}})
	term.owe(term.scan.find([]byte("\x1b[c")))
	term.afterAnswerLock = func() {
		term.mu.Lock()
		term.latch(ScreenInvalid, "a gap")
		term.mu.Unlock()
		term.payOwed()
	}
	term.answer()
	term.payOwed()
	if got := written(); got != answerDA1 {
		t.Fatalf("written %q, want one DA", got)
	}
}

// reviewer-codex (2): a latch during a pass (left the pending answers to
// it), then Replies panics: the pending DA is owed and written once.
func TestLatchThenRepliesPanicPaysOnce(t *testing.T) {
	f := &faultScreen{Screen: screen.New(20, 5, 10), replies: [][]byte{nil}}
	term, written := pipeTerm(t, f)
	term.owe(term.scan.find([]byte("\x1b[c")))
	f.beforeReplies = func() {
		term.mu.Lock()
		term.latch(ScreenStuck, "a stuck latch")
		term.mu.Unlock()
	}
	term.answer()
	term.payOwed()
	if got := written(); got != answerDA1 {
		t.Fatalf("written %q, want one DA", got)
	}
}

// Answer passes never overlap: while one runs (here, paused after its
// Replies, the emulator's lock released), another returns at once, so
// answering is one pass's.
func TestAnswerPassesNeverOverlap(t *testing.T) {
	f := &faultScreen{Screen: screen.New(20, 5, 10), replies: [][]byte{[]byte(answerDA1), []byte(answerDA1)}}
	term, written := pipeTerm(t, f)
	term.owe(term.scan.find([]byte("\x1b[c")))
	inside, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	term.afterReplies = func() { // the first pass, its emulator lock released
		if first.CompareAndSwap(false, true) {
			close(inside)
			<-release
		}
	}
	done := make(chan struct{})
	go func() { term.answer(); close(done) }()
	<-inside
	term.answer() // a second pass while the first runs: returns, collects nothing
	close(release)
	<-done
	if got := written(); got != answerDA1 {
		t.Fatalf("written %q, want the one DA of the first pass", got)
	}
	f.mu.Lock()
	left := len(f.replies)
	f.mu.Unlock()
	if left != 1 {
		t.Fatalf("Replies called %d times", 2-left)
	}
}
