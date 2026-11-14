package holder

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const wakeText = "[agentbus#a1b2c3] demo: 2 message(s) waiting for fake. Run: agentbus --project demo subscribe --drain fake"

func submitHolder(t *testing.T, set ...func(*Config)) string {
	t.Helper()
	_, sock := newTestHolderWith(t, func(c *Config) {
		c.SubmitWait, c.PersonQuiet = time.Second, 200*time.Millisecond
		for _, f := range set {
			f(c)
		}
	})
	return sock
}

func startFake(t *testing.T, cl *Client, role string) TermInfo {
	t.Helper()
	r, err := cl.Call(Start{Role: role, Cols: 200, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	waitScreen(t, cl, r.Term.ID, "────")
	return *r.Term
}

func submitted(t *testing.T, cl *Client, id string) []string {
	t.Helper()
	r, err := cl.Call(ScreenReq{Term: id})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range r.Screen.Lines {
		if s, ok := strings.CutPrefix(l, "SUBMITTED: "); ok {
			out = append(out, strings.TrimRight(s, " "))
		}
	}
	return out
}

func codeOf(err error) string {
	var re *RemoteError
	if errors.As(err, &re) {
		return re.Code
	}
	if err != nil {
		return err.Error()
	}
	return "ok"
}

func wake(nonce string) Submit { return Submit{Kind: "wake", Term: "t1", Nonce: nonce, Count: 2} }

// The line lands (spec 4.2): pasted, seen in the input, then CR; the holder
// composes it from its own project and the terminal's name.
func TestSubmitLands(t *testing.T) {
	sock := submitHolder(t)
	cl := dial(t, sock)
	startFake(t, cl, "fake")
	if _, err := cl.Call(wake("a1b2c3")); err != nil {
		t.Fatal(err)
	}
	waitScreen(t, cl, "t1", "SUBMITTED: "+wakeText)
	if _, err := cl.Call(Submit{Kind: "clear", Term: "t1"}); err != nil {
		t.Fatal(err)
	}
	waitScreen(t, cl, "t1", "SUBMITTED: /clear")
	if got := submitted(t, cl, "t1"); len(got) != 2 {
		t.Fatalf("submitted %q", got)
	}
}

// A person at the terminal (spec 4.5): a typing viewer attached and not
// proven unfocused, a draft however old, or typing within person_quiet:
// refused, nothing typed. A CR ends the draft.
func TestSubmitRefusesAPerson(t *testing.T) {
	sock := submitHolder(t)
	cl := dial(t, sock)
	startFake(t, cl, "fake")
	v := attached(t, sock, Attach{Term: "t1", Cols: 200, Rows: 30})
	if _, err := cl.Call(wake("a1b2c3")); codeOf(err) != CodePersonActive {
		t.Fatalf("a focused viewer: %v", err)
	}
	control(t, v, Focus{In: false})
	time.Sleep(50 * time.Millisecond)
	send(t, v, FrameData, []byte("x"))
	time.Sleep(400 * time.Millisecond) // past person_quiet: the draft alone stands
	if _, err := cl.Call(wake("a1b2c3")); codeOf(err) != CodePersonActive {
		t.Fatalf("a draft: %v", err)
	}
	send(t, v, FrameData, []byte("\r"))
	waitScreen(t, cl, "t1", "SUBMITTED: x")
	if _, err := cl.Call(wake("a1b2c3")); codeOf(err) != CodePersonActive {
		t.Fatalf("typing within person_quiet: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := cl.Call(wake("a1b2c3")); err != nil {
		t.Fatalf("no person: %v", err)
	}
	waitScreen(t, cl, "t1", "SUBMITTED: "+wakeText)
}

// A viewer typing a sentence and Enter during the submit (spec 8): the line
// is submitted alone, then the viewer's bytes arrive in order after the CR.
func TestSubmitHoldsViewerBytes(t *testing.T) {
	sock := submitHolder(t)
	cl := dial(t, sock)
	startFake(t, cl, "fake-slow") // the paste shows after 300 ms: the submit is long
	v := attached(t, sock, Attach{Term: "t1", Cols: 200, Rows: 30})
	control(t, v, Focus{In: false})
	time.Sleep(50 * time.Millisecond)
	var wg sync.WaitGroup
	wg.Add(1)
	var err error
	go func() { defer wg.Done(); _, err = cl.Call(wake("a1b2c3")) }()
	time.Sleep(150 * time.Millisecond) // inside the critical section
	send(t, v, FrameData, []byte("hello there\r"))
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	waitScreen(t, cl, "t1", "SUBMITTED: hello there")
	slowText := strings.ReplaceAll(wakeText, "fake", "fake-slow")
	if got := submitted(t, cl, "t1"); len(got) != 2 || got[0] != slowText || got[1] != "hello there" {
		t.Fatalf("submitted %q", got)
	}
}

// Security S4: a program without bracketed paste never receives a paste.
func TestSubmitNeedsBracketedPaste(t *testing.T) {
	sock := submitHolder(t)
	cl := dial(t, sock)
	startFake(t, cl, "fake-no2004")
	if _, err := cl.Call(wake("a1b2c3")); codeOf(err) != CodeNotReady {
		t.Fatalf("no mode 2004: %v", err)
	}
	if r, _ := cl.Call(ScreenReq{Term: "t1"}); strings.Contains(strings.Join(r.Screen.Lines, "\n"), "agentbus#") {
		t.Fatal("something was typed")
	}
}

// A dialog opening between the first check and the recheck (spec 4.2 step
// 3): nothing typed.
func TestSubmitRechecks(t *testing.T) {
	sock := submitHolder(t, func(c *Config) {
		c.afterCheck = func(t *terminal) {
			t.pty.Write([]byte{0x04}) // the program opens a menu
			time.Sleep(100 * time.Millisecond)
		}
	})
	cl := dial(t, sock)
	startFake(t, cl, "fake")
	if _, err := cl.Call(wake("a1b2c3")); codeOf(err) != CodeNotReady {
		t.Fatalf("a dialog in the window: %v", err)
	}
	if got := submitted(t, cl, "t1"); len(got) != 0 {
		t.Fatalf("submitted %q", got)
	}
}

// Program-placed input text (spec 8): no viewer byte since the CR, yet the
// input is not empty: refused, nothing typed.
func TestSubmitRefusesProgramPlacedText(t *testing.T) {
	sock := submitHolder(t)
	cl := dial(t, sock)
	startFake(t, cl, "fake-return")
	if _, err := cl.Call(wake("a1b2c3")); err != nil {
		t.Fatal(err)
	}
	waitScreen(t, cl, "t1", "> a queued message")
	if _, err := cl.Call(wake("d4e5f6")); codeOf(err) != CodeNotReady {
		t.Fatalf("a returned message: %v", err)
	}
}

// A line that does not land (spec 4.2 step 6, S5): no CR, the terminal left
// behind (the next submit refused), every viewer byte held and the viewers
// told, until <prefix> r (release) delivers them or <prefix> x (discard)
// drops them; a settled empty input lifts the mark.
func TestSubmitNotLanded(t *testing.T) {
	sock := submitHolder(t)
	cl := dial(t, sock)
	startFake(t, cl, "fake-dialog") // a paste opens a menu: never seen in the input
	v := attached(t, sock, Attach{Term: "t1", Cols: 200, Rows: 30})
	control(t, v, Focus{In: false})
	time.Sleep(300 * time.Millisecond)
	if _, err := cl.Call(Submit{Kind: "clear", Term: "t1"}); codeOf(err) != CodeNotLanded {
		t.Fatalf("not seen: %v", err)
	}
	if _, err := cl.Call(wake("a1b2c3")); codeOf(err) != CodeLeftBehind {
		t.Fatalf("left behind: %v", err)
	}
	if got := submitted(t, cl, "t1"); len(got) != 0 {
		t.Fatalf("a CR was sent: %q", got)
	}
	send(t, v, FrameData, []byte("2\r")) // would answer the menu: held
	notice := readNotice(t, v, func(n HoldNotice) bool { return n.Held == 2 })
	if !notice.Active || notice.Kind != "clear" {
		t.Fatalf("notice %+v", notice)
	}
	late := attached(t, sock, Attach{Term: "t1", Cols: 200, Rows: 30}) // one attaching during the hold is told too
	if n := readNotice(t, late, func(HoldNotice) bool { return true }); !n.Active || n.Held != 2 {
		t.Fatalf("late viewer's notice %+v", n)
	}
	time.Sleep(300 * time.Millisecond)
	if r, _ := cl.Call(ScreenReq{Term: "t1"}); !strings.Contains(strings.Join(r.Screen.Lines, "\n"), "> 1. Yes") {
		t.Fatal("a held byte reached the menu")
	}
	control(t, v, Discard{}) // dropped: the menu stays
	readNotice(t, v, func(n HoldNotice) bool { return !n.Active })
	time.Sleep(400 * time.Millisecond)
	if r, _ := cl.Call(ScreenReq{Term: "t1"}); !strings.Contains(strings.Join(r.Screen.Lines, "\n"), "> 1. Yes") {
		t.Fatal("a discarded byte reached the menu")
	}
	if _, err := cl.Call(wake("a1b2c3")); codeOf(err) != CodeLeftBehind {
		t.Fatalf("still left behind after a discard: %v", err)
	}
	// The person closes the menu: a settled empty input lifts the mark.
	send(t, v, FrameData, []byte("\r"))
	eventually(t, "the mark lifted", func() bool {
		_, err := cl.Call(Ping{})
		r, _ := cl.Call(ScreenReq{Term: "t1"})
		return err == nil && !strings.Contains(strings.Join(r.Screen.Lines, "\n"), "1. Yes") && leftBehindOff(t, sock)
	})
}

// leftBehindOff: a submit is no longer refused as left behind (a probe that
// lands nowhere: the fake-dialog program opens its menu again, so the probe
// itself is not_landed or later).
func leftBehindOff(t *testing.T, sock string) bool {
	cl := dial(t, sock)
	_, err := cl.Call(Submit{Kind: "wake", Term: "t1", Nonce: "fffff0", Count: 1})
	return codeOf(err) != CodeLeftBehind
}

// readNotice reads the stream until a hold notice matching ok.
func readNotice(t *testing.T, cl *Client, ok func(HoldNotice) bool) HoldNotice {
	t.Helper()
	cl.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer cl.c.SetReadDeadline(time.Time{})
	for {
		typ, body, err := ReadFrame(cl.c)
		if err != nil {
			t.Fatalf("no notice: %v", err)
		}
		if typ != FrameControl {
			continue
		}
		v, err := DecodeControl(body)
		if n, isN := v.(HoldNotice); err == nil && isN && ok(n) {
			return n
		}
	}
}

// Release delivers the held bytes in order.
func TestSubmitReleaseDelivers(t *testing.T) {
	sock := submitHolder(t)
	cl := dial(t, sock)
	startFake(t, cl, "fake-dialog")
	v := attached(t, sock, Attach{Term: "t1", Cols: 200, Rows: 30})
	control(t, v, Focus{In: false})
	time.Sleep(300 * time.Millisecond)
	if _, err := cl.Call(wake("a1b2c3")); codeOf(err) != CodeNotLanded {
		t.Fatalf("not seen: %v", err)
	}
	send(t, v, FrameData, []byte("\rab")) // closes the menu once released
	send(t, v, FrameData, []byte("cd"))
	readNotice(t, v, func(n HoldNotice) bool { return n.Held == 5 && n.Kind == "wake" })
	control(t, v, Release{})
	waitScreen(t, cl, "t1", "> abcd")
}

// The hold's own end (spec 4.2): bytes held when a settled screen shows the
// input empty are delivered then, in order, and the mark lifts.
func TestSubmitEmptyInputReleases(t *testing.T) {
	sock := submitHolder(t)
	cl := dial(t, sock)
	startFake(t, cl, "fake-dialog-close") // the menu closes by itself after 1.5 s
	v := attached(t, sock, Attach{Term: "t1", Cols: 200, Rows: 30})
	control(t, v, Focus{In: false})
	time.Sleep(300 * time.Millisecond)
	if _, err := cl.Call(wake("a1b2c3")); codeOf(err) != CodeNotLanded {
		t.Fatalf("not seen: %v", err)
	}
	send(t, v, FrameData, []byte("xyz"))
	readNotice(t, v, func(n HoldNotice) bool { return n.Held == 3 })
	readNotice(t, v, func(n HoldNotice) bool { return !n.Active })
	waitScreen(t, cl, "t1", "> xyz")
	if _, err := cl.Call(wake("d4e5f6")); codeOf(err) == CodeLeftBehind {
		t.Fatal("the mark stands on an empty input")
	}
}

// The landing check reads this line's own token (agentbus#<nonce>): another
// wake line's in the input is not this one landed (spec 4.2 step 5).
func TestSubmitLandsOnlyItsOwnToken(t *testing.T) {
	sock := submitHolder(t)
	cl := dial(t, sock)
	startFake(t, cl, "fake-stale")
	if _, err := cl.Call(wake("a1b2c3")); codeOf(err) != CodeNotLanded {
		t.Fatalf("another token in the input: %v", err)
	}
	if got := submitted(t, cl, "t1"); len(got) != 0 {
		t.Fatalf("a CR was sent: %q", got)
	}
}

// A lone ESC still waiting for its sequence (5c's escWait) is the person at
// the terminal: the Escape key or a key's sequence still arriving, never
// nobody (spec 4.5). Once it resolves as the Escape key, typed and draft
// stand as for any key.
func TestSubmitRefusesAPendingEscape(t *testing.T) {
	sock := submitHolder(t, func(c *Config) { c.escWait = time.Hour })
	cl := dial(t, sock)
	startFake(t, cl, "fake")
	v := attached(t, sock, Attach{Term: "t1", Cols: 200, Rows: 30})
	control(t, v, Focus{In: false})
	time.Sleep(50 * time.Millisecond)
	send(t, v, FrameData, []byte{0x1b})
	time.Sleep(50 * time.Millisecond)
	if _, err := cl.Call(wake("a1b2c3")); codeOf(err) != CodePersonActive {
		t.Fatalf("a pending ESC: %v", err)
	}
	if got := submitted(t, cl, "t1"); len(got) != 0 {
		t.Fatalf("submitted %q", got)
	}
}

// The same with kitty's Enter encoding (CSI 13 u, spec 8): held, then after
// the CR, and it ends the draft as a CR does.
func TestSubmitHoldsAKittyEnter(t *testing.T) {
	sock := submitHolder(t)
	cl := dial(t, sock)
	startFake(t, cl, "fake-slow")
	v := attached(t, sock, Attach{Term: "t1", Cols: 200, Rows: 30})
	control(t, v, Focus{In: false})
	time.Sleep(50 * time.Millisecond)
	var wg sync.WaitGroup
	wg.Add(1)
	var err error
	go func() { defer wg.Done(); _, err = cl.Call(wake("a1b2c3")) }()
	time.Sleep(150 * time.Millisecond)
	send(t, v, FrameData, []byte("hello\x1b[13u"))
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	waitScreen(t, cl, "t1", "SUBMITTED: hello")
	slowText := strings.ReplaceAll(wakeText, "fake", "fake-slow")
	if got := submitted(t, cl, "t1"); len(got) != 2 || got[0] != slowText || got[1] != "hello" {
		t.Fatalf("submitted %q", got)
	}
	time.Sleep(400 * time.Millisecond) // past person_quiet: no draft stands
	if _, err := cl.Call(wake("d4e5f6")); err != nil {
		t.Fatalf("after a kitty Enter: %v", err)
	}
}

// An observer never blocks a submit (spec 8, focus rule): it sends no focus
// report and the line still goes.
func TestSubmitIgnoresAnObserver(t *testing.T) {
	sock := submitHolder(t)
	cl := dial(t, sock)
	startFake(t, cl, "fake")
	attached(t, sock, Attach{Term: "t1", Observe: true, Cols: 200, Rows: 30})
	time.Sleep(50 * time.Millisecond)
	if _, err := cl.Call(wake("a1b2c3")); err != nil {
		t.Fatalf("an observer: %v", err)
	}
	waitScreen(t, cl, "t1", "SUBMITTED: "+wakeText)
}

// A full hold queue refuses further bytes, never stops reading the viewer
// (spec 4.2, correction r25): what is held stays held and is delivered in
// order; the refused bytes are counted, never delivered; the count ends with
// the hold.
func TestHoldFullRefuses(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	tm := &terminal{pty: w, viewers: map[*viewer]bool{}, holdState: holdState{holdMax: holdMax}}
	tm.inMu.Lock()
	tm.startHold("wake")
	tm.inMu.Unlock()
	first := make([]byte, holdMax) // distinct bytes: a reordering shows
	for i := range first {
		first[i] = byte('a' + i%26)
	}
	tm.deliver(first)
	done := make(chan struct{})
	go func() { tm.deliver([]byte("bc")); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a full queue blocks its viewer")
	}
	tm.mu.Lock()
	refused := tm.refused
	tm.mu.Unlock()
	if refused != 2 {
		t.Fatalf("refused %d", refused)
	}
	got := make(chan []byte)
	go func() { b, _ := io.ReadAll(r); got <- b }()
	tm.endHold(true)
	if tm.refused != 0 {
		t.Fatalf("refused %d after the hold", tm.refused)
	}
	w.Close()
	if b := <-got; !bytes.Equal(b, first) {
		t.Fatalf("delivered %d bytes, tail %q", len(b), b[max(0, len(b)-3):])
	}
}

// End to end (foureyes P1): one viewer fills the queue past its bound, then
// sends Release on the same socket: the Release gets through, the held bytes
// arrive in order, the refused ones never, and the viewer was told.
func TestHoldFullViewerStillReleases(t *testing.T) {
	sock := submitHolder(t, func(c *Config) { c.holdMax = 8 })
	cl := dial(t, sock)
	startFake(t, cl, "fake-dialog")
	v := attached(t, sock, Attach{Term: "t1", Cols: 200, Rows: 30})
	control(t, v, Focus{In: false})
	time.Sleep(300 * time.Millisecond)
	if _, err := cl.Call(wake("a1b2c3")); codeOf(err) != CodeNotLanded {
		t.Fatalf("not seen: %v", err)
	}
	send(t, v, FrameData, []byte("\rabc")) // the \r closes the menu once released
	send(t, v, FrameData, []byte("defg"))
	send(t, v, FrameData, []byte("XYZ")) // past 8: refused
	n := readNotice(t, v, func(n HoldNotice) bool { return n.Refused > 0 })
	if !n.Active || n.Held != 8 || n.Refused != 3 {
		t.Fatalf("notice %+v", n)
	}
	control(t, v, Release{})
	readNotice(t, v, func(n HoldNotice) bool { return !n.Active })
	waitScreen(t, cl, "t1", "> abcdefg")
	time.Sleep(200 * time.Millisecond)
	if r, _ := cl.Call(ScreenReq{Term: "t1"}); strings.Contains(strings.Join(r.Screen.Lines, "\n"), "XYZ") {
		t.Fatal("a refused byte was delivered")
	}
	control(t, v, Detach{}) // and the viewer is still read: its detach ends the attach
	eventually(t, "the detach", func() bool { return activityOf(t, cl, "t1").Viewers == 0 })
}

// submit_wait bounds the landing check (spec 4.2 step 5): a screen that
// never settles after the paste does not stretch it by settleMax.
func TestSubmitWaitBoundsTheLanding(t *testing.T) {
	sock := submitHolder(t, func(c *Config) { c.SubmitWait = 300 * time.Millisecond })
	cl := dial(t, sock)
	startFake(t, cl, "fake-busy")
	start := time.Now()
	if _, err := cl.Call(wake("a1b2c3")); codeOf(err) != CodeNotLanded {
		t.Fatalf("a busy screen: %v", err)
	}
	if d := time.Since(start); d > 300*time.Millisecond+2*settleQuiet+settleMax/2 {
		t.Fatalf("the submit took %v for a submit_wait of 300ms", d)
	}
}
