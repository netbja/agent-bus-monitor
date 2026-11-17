package holder

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/screen"
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
			`printf '\033[6n'; timeout 1 dd bs=1 count=1 2>/dev/null > ` + dir + `/cpr; ` +
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
