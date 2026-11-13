package holder

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/screen"
)

// attached is a viewer's connection: Call(Attach) answered, then the
// stream.
func attached(t *testing.T, sock string, a Attach) *Client {
	t.Helper()
	cl := dial(t, sock)
	if _, err := cl.Call(a); err != nil {
		t.Fatalf("attach %+v: %v", a, err)
	}
	return cl
}

// readUntil reads the stream's data frames until they hold want; it
// returns everything read.
func readUntil(t *testing.T, cl *Client, want string) string {
	t.Helper()
	var got strings.Builder
	cl.c.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer cl.c.SetReadDeadline(time.Time{})
	for !strings.Contains(got.String(), want) {
		typ, body, err := ReadFrame(cl.c)
		if err != nil {
			t.Fatalf("stream ended before %q: %v\n%q", want, err, got.String())
		}
		if typ == FrameData {
			got.Write(body)
		}
	}
	return got.String()
}

func send(t *testing.T, cl *Client, typ byte, b []byte) {
	t.Helper()
	if err := WriteFrame(cl.c, typ, b); err != nil {
		t.Fatal(err)
	}
}

func control(t *testing.T, cl *Client, v any) {
	t.Helper()
	b, err := EncodeControl(v)
	if err != nil {
		t.Fatal(err)
	}
	send(t, cl, FrameControl, b)
}

func activityOf(t *testing.T, cl *Client, id string) ActivityInfo {
	t.Helper()
	r, err := cl.Call(ActivityReq{Term: id})
	if err != nil {
		t.Fatal(err)
	}
	return *r.Activity
}

// eventually polls cond for up to 5 s.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("never: %s", what)
}

// An attach (spec 4.6): a first paint from the emulator, then the output
// live; the viewer's bytes reach the program. Its typing and the draft
// (spec 4.5): a byte starts a draft, an Enter ends it, a mouse or focus
// report is no typing. A viewer counts as focused from the moment it
// attaches until a focus-out.
func TestHolderAttach(t *testing.T) {
	_, sock := newTestHolder(t)
	cl := dial(t, sock)
	x := startRole(t, cl, "talker")
	waitScreen(t, cl, x.ID, "ready")
	v := attached(t, sock, Attach{Term: x.ID, Cols: 100, Rows: 30})
	readUntil(t, v, "ready") // the first paint
	a := activityOf(t, cl, x.ID)
	if a.Viewers != 1 || a.FocusedViewers != 1 || a.Draft || a.ViewerTypedAt != 0 {
		t.Fatalf("attached: %+v", a)
	}
	if termOf(t, cl, x.ID).Viewers != 1 {
		t.Fatal("List counts no viewer")
	}
	send(t, v, FrameData, []byte("abc"))
	readUntil(t, v, "abc") // the program's echo, live
	a = activityOf(t, cl, x.ID)
	if !a.Draft || a.DraftSince == 0 || a.ViewerTypedAt == 0 {
		t.Fatalf("typed: %+v", a)
	}
	send(t, v, FrameData, []byte("\r"))
	eventually(t, "the draft ends at Enter", func() bool { return !activityOf(t, cl, x.ID).Draft })
	typedAt := activityOf(t, cl, x.ID).ViewerTypedAt
	time.Sleep(20 * time.Millisecond) // a later typing would show a later time
	send(t, v, FrameData, []byte("\x1b[<0;5;5M\x1b[<0;5;5m"))
	time.Sleep(100 * time.Millisecond)
	if a := activityOf(t, cl, x.ID); a.Draft || a.ViewerTypedAt != typedAt {
		t.Fatalf("a mouse report counted as typing: %+v (typed at %d)", a, typedAt)
	}
	control(t, v, Focus{In: false})
	eventually(t, "focus-out", func() bool { return activityOf(t, cl, x.ID).FocusedViewers == 0 })
	control(t, v, Focus{In: true})
	eventually(t, "focus-in", func() bool { return activityOf(t, cl, x.ID).FocusedViewers == 1 })
	control(t, v, Detach{})
	eventually(t, "the detach", func() bool { return activityOf(t, cl, x.ID).Viewers == 0 })
	var re *RemoteError
	if _, err := cl.Call(Attach{Term: "t99"}); !errors.As(err, &re) || re.Code != CodeUnknown {
		t.Fatalf("an unknown terminal: %v", err)
	}
	if _, err := cl.Call(Focus{In: true}); !errors.As(err, &re) || re.Code != CodeBadFrame {
		t.Fatalf("a focus outside an attach: %v", err)
	}
}

// The output filter on the stream (security S3): the program's OSC 52, OSC
// 8 link, title report and cursor query never reach the viewer, the link's
// text does; with attach.osc52 on, OSC 52 passes.
func TestHolderAttachFilters(t *testing.T) {
	for _, on := range []bool{false, true} {
		_, sock := newTestHolderWith(t, func(c *Config) { c.OSC52 = on })
		cl := dial(t, sock)
		x := startRole(t, cl, "osc")
		waitScreen(t, cl, x.ID, "ready")
		v := attached(t, sock, Attach{Term: x.ID})
		readUntil(t, v, "ready")
		send(t, v, FrameData, []byte("go\r"))
		got := readUntil(t, v, "|end")
		for _, bad := range []string{"https://evil.example", "\x1b[21t", "\x1b[6n", "\x1b]8;"} {
			if strings.Contains(got, bad) {
				t.Errorf("osc52 %v: %q reached the viewer: %q", on, bad, got)
			}
		}
		if !strings.Contains(got, "link") {
			t.Errorf("the link's text is gone: %q", got)
		}
		if strings.Contains(got, "\x1b]52;c;aGk=") != on {
			t.Errorf("osc52 %v: OSC 52 in the stream %v: %q", on, !on, got)
		}
	}
}

// An observer (spec 4.6): never types, never resizes, never counts as a
// person at the terminal. The most recently active viewer sets the size.
func TestHolderObserver(t *testing.T) {
	_, sock := newTestHolder(t)
	cl := dial(t, sock)
	x := startRole(t, cl, "winch")
	waitScreen(t, cl, x.ID, "w")
	ob := attached(t, sock, Attach{Term: x.ID, Observe: true, Cols: 70, Rows: 20})
	readUntil(t, ob, "w")
	send(t, ob, FrameData, []byte("zzz\r"))
	control(t, ob, Resize{Cols: 50, Rows: 10})
	time.Sleep(300 * time.Millisecond)
	if a := activityOf(t, cl, x.ID); a.Viewers != 1 || a.FocusedViewers != 0 || a.Draft || a.ViewerTypedAt != 0 {
		t.Fatalf("an observer at the terminal: %+v", a)
	}
	if r, _ := cl.Call(ScreenReq{Term: x.ID}); strings.Contains(strings.Join(r.Screen.Lines, "\n"), "zzz") || strings.Contains(strings.Join(r.Screen.Lines, "\n"), "10 50") ||
		strings.Contains(strings.Join(r.Screen.Lines, "\n"), "20 70") {
		t.Fatalf("the observer typed or resized:\n%s", strings.Join(r.Screen.Lines, "\n"))
	}
	v := attached(t, sock, Attach{Term: x.ID, Cols: 90, Rows: 30})
	waitScreen(t, cl, x.ID, "30 90") // the viewer's attach sets the size
	control(t, v, Resize{Cols: 91, Rows: 31})
	waitScreen(t, cl, x.ID, "31 91")
}

// The stream ends with the program.
func TestHolderAttachEndsWithTheProgram(t *testing.T) {
	_, sock := newTestHolder(t)
	cl := dial(t, sock)
	x := startRole(t, cl, "exiter")
	v := attached(t, sock, Attach{Term: x.ID, Observe: true})
	v.c.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		if _, _, err := ReadFrame(v.c); err != nil {
			var ne interface{ Timeout() bool }
			if errors.As(err, &ne) && ne.Timeout() {
				t.Fatal("the stream outlived its program")
			}
			return
		}
	}
}

// The viewer's input scanner (spec 4.5): what is typing and what ends a
// draft, however the viewer's bytes are cut across frames (reviewer-codex:
// a kitty Enter in two frames; foureyes: the viewer terminal's own replies
// are no typing).
func TestInputScan(t *testing.T) {
	const st = "\x1b\\"
	for _, c := range []struct {
		before bool
		in     string
		draft  bool
		typed  bool
	}{
		{false, "a", true, true},
		{true, "\r", false, true},
		{false, "abc\r", false, true},
		{false, "\rabc", true, true},
		{true, "\x1b[13u", false, true},
		{true, "\x1b[13;1u", false, true},
		{true, "abc\x1b[13u", false, true},
		{false, "\x1b[A", true, true},
		{false, "\x1b[15~", true, true},
		{false, "\x1bOP", true, true},
		{false, "\x1bx", true, true},
		{false, "\x1b[97u", true, true},
		{false, "\x1b[<0;5;5M", false, false},
		{true, "\x1b[<0;5;5m", true, false},
		{false, "\x1b[M !!", false, false},
		{false, "\x1b[I\x1b[O", false, false},
		// The viewer terminal's replies to queries: never typing.
		{false, "\x1b[?2026;2$y", false, false},          // DECRQM
		{false, "\x1bP>|ghostty 1.2" + st, false, false}, // XTVERSION
		{false, "\x1b[?997;1n", false, false},            // colour scheme report
		{false, "\x1b[0n", false, false},                 // DSR status
		{false, "\x1b[12;40R", false, false},             // DSR cursor
		{false, "\x1b[?62;22c", false, false},            // DA
		{false, "\x1b[4;800;1200t", false, false},        // window size report
		{false, "\x1b]11;rgb:0000/0000/0000\x07", false, false},
		{false, "\x1b]11;rgb:0000/0000/0000" + st, false, false},
		{false, "\x1b_Gi=1;OK" + st, false, false}, // kitty graphics reply
		{true, "\x1b[?2026;2$y", true, false},      // a reply leaves a draft as it was
		{false, "\x1b[?2026;2$yhi", true, true},
		{false, "", false, false},
	} {
		for cut := 0; cut <= len(c.in); cut++ {
			var s inputScan
			d, t1 := s.feed(c.before, []byte(c.in[:cut]))
			d, t2 := s.feed(d, []byte(c.in[cut:]))
			if d != c.draft || (t1 || t2) != c.typed {
				t.Errorf("%q (cut at %d) from %v: draft %v typed %v, want %v %v", c.in, cut, c.before, d, t1 || t2, c.draft, c.typed)
				break
			}
		}
	}
}

// A viewer that falls behind is repainted, never blocking the program: its
// stale chunks dropped, one first paint instead.
func TestSlowViewerIsRepainted(t *testing.T) {
	v := &viewer{out: make(chan []byte, 2)}
	term := &terminal{viewers: map[*viewer]bool{v: true}, scr: screen.New(20, 5, 10)}
	term.fanOut([]byte("one"))
	term.fanOut([]byte("two"))
	term.fanOut([]byte("three")) // the queue holds two: a repaint
	if !v.repaint {
		t.Fatal("a full queue did not mark the repaint")
	}
	term.fanOut([]byte("four")) // skipped: the repaint covers it
	if len(v.out) != 2 {
		t.Fatalf("queue %d", len(v.out))
	}
	b, ok := term.nextFor(v, nil, nil)
	if !ok || v.repaint || len(v.out) != 0 || !strings.Contains(string(b), "\x1b[") {
		t.Fatalf("repaint %q ok %v queue %d", b, ok, len(v.out))
	}
}
