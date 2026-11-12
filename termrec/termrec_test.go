package termrec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/term"
)

// The fake client: the test binary itself, run under the recorder's PTY with
// TERMREC_FAKE set. It prints a prompt, echoes what it reads, writes its size
// on SIGWINCH-free demand ("?"), sends one hook line when it starts, ignores
// F12 unless TERMREC_FAKE=bound, and quits on "q".
func TestMain(m *testing.M) {
	if mode := os.Getenv("TERMREC_FAKE"); mode != "" {
		fakeClient(mode)
		return
	}
	os.Exit(m.Run())
}

func fakeClient(mode string) {
	if p := os.Getenv(HookEnv); p != "" {
		if c, err := net.Dial("unix", p); err == nil {
			fmt.Fprintln(c, `{"hook_event_name":"SessionStart"}`)
			c.Close()
		}
	}
	if mode == "noisy" { // never quiet: a spinner
		go func() {
			for {
				os.Stdout.WriteString(".")
				time.Sleep(40 * time.Millisecond)
			}
		}()
	}
	old, _ := term.MakeRaw(0)
	defer term.Restore(0, old)
	os.Stdout.WriteString("ready> ")
	r := bufio.NewReader(os.Stdin)
	var pending []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			return
		}
		pending = append(pending, b)
		s := string(pending)
		switch {
		case s == "q":
			os.Stdout.WriteString("\r\nbye\r\n")
			return
		case s == "?":
			w, h, _ := term.GetSize(0)
			fmt.Fprintf(os.Stdout, "[%dx%d]", w, h)
		case s == "\x1b[24~" || s == "\x1b[24;1:1~":
			if mode == "bound" {
				os.Stdout.WriteString("[F12]")
			}
		case strings.HasPrefix(s, "\x1b") && len(s) < 12:
			continue // an escape sequence still arriving
		default:
			os.Stdout.Write(pending)
		}
		pending = pending[:0]
	}
}

func fakeArgv(t *testing.T) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return []string{exe, "-test.run=^$"}
}

func record(t *testing.T, mode, script string) Recording {
	t.Helper()
	steps, err := ParseScript(script)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err = Record(ctx, &buf, Options{
		Client: "fake", Fixture: "probe", Argv: fakeArgv(t), Cols: 80, Rows: 24,
		Env: []string{"TERMREC_FAKE=" + mode}, Steps: steps,
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	rec, err := Read(&buf)
	if err != nil {
		t.Fatalf("read back: %v\n%s", err, buf.String())
	}
	return rec
}

// A recording keeps the header, the output, what was typed, the resizes, the
// marks and the hook lines, in time order.
func TestRecordAFakeClient(t *testing.T) {
	rec := record(t, "free", `
quiet 300ms max 5s
mark start
type abc
quiet 300ms max 5s
resize 100x30
type ?
quiet 300ms max 5s
mark before-f12
key f12
key f12-kitty
sleep 500ms
mark after-f12
type q
quiet 300ms max 5s
`)
	if rec.Header.V != Version || rec.Header.Client != "fake" || rec.Header.Fixture != "probe" ||
		rec.Header.Cols != 80 || rec.Header.Rows != 24 || len(rec.Header.Argv) == 0 || rec.Header.Start.IsZero() {
		t.Fatalf("header %+v", rec.Header)
	}
	out := string(rec.Output())
	for _, w := range []string{"ready> ", "abc", "[100x30]", "bye"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q: %q", w, out)
		}
	}
	var kinds []string
	var last int64 = -1
	hook := false
	for _, e := range rec.Events {
		if e.T < last {
			t.Fatalf("events out of order at %+v", e)
		}
		last = e.T
		if len(kinds) == 0 || kinds[len(kinds)-1] != e.Kind {
			kinds = append(kinds, e.Kind)
		}
		if e.Kind == KindHook && strings.Contains(e.Text, "SessionStart") {
			hook = true
		}
	}
	if !hook {
		t.Errorf("no hook line recorded: %v", kinds)
	}
	in := ""
	for _, e := range rec.Events {
		if e.Kind == KindInput {
			in += string(e.Bytes())
		}
	}
	if in != "abc?\x1b[24~\x1b[24;1:1~q" {
		t.Errorf("input %q", in)
	}
	if b, ok := rec.OutputBetween("before-f12", "after-f12"); !ok || len(b) != 0 {
		t.Errorf("a free F12 wrote %q (%v)", b, ok)
	}
	if e := rec.Events[len(rec.Events)-1]; e.Kind != KindExit {
		t.Errorf("last event %+v, want the exit", e)
	}
}

// A client that binds F12 writes something between the marks.
func TestRecordABoundKey(t *testing.T) {
	rec := record(t, "bound", "quiet 300ms max 5s\nmark before-f12\nkey f12\nsleep 500ms\nmark after-f12\ntype q\nquiet 300ms max 5s\n")
	if b, ok := rec.OutputBetween("before-f12", "after-f12"); !ok || !strings.Contains(string(b), "[F12]") {
		t.Fatalf("bound F12: %q (%v)", b, ok)
	}
	if _, ok := rec.OutputBetween("before-f12", "nope"); ok {
		t.Fatal("a missing mark read as present")
	}
}

// A client still running when the script ends is stopped; the recording
// says how it ended.
func TestRecordStopsALingeringClient(t *testing.T) {
	rec := record(t, "free", "quiet 200ms max 5s\n")
	e := rec.Events[len(rec.Events)-1]
	if e.Kind != KindExit || !strings.Contains(e.Text, "stopped") {
		t.Fatalf("last event %+v", e)
	}
}

func TestParseScript(t *testing.T) {
	steps, err := ParseScript("# a comment\n\ntype hello world\nkey enter\nkey ctrl+u\nsleep 2s\nquiet 800ms max 10s\nmark x\nresize 120x40\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 7 || steps[0].Bytes != "hello world" || steps[1].Bytes != "\r" || steps[2].Bytes != "\x15" ||
		steps[3].Wait != 2*time.Second || steps[4].Wait != 800*time.Millisecond || steps[4].Max != 10*time.Second ||
		steps[5].Mark != "x" || steps[6].Cols != 120 || steps[6].Rows != 40 {
		t.Fatalf("steps %+v", steps)
	}
	for _, bad := range []string{"jump", "key f13", "key ctrl+1", "sleep forever", "quiet 1s max", "resize 0x10", "mark", "type", "quiet 2s max 1s", "sleep 2h"} {
		if _, err := ParseScript(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// F1-F12 in the legacy encoding and in kitty's (press event reported).
func TestKeys(t *testing.T) {
	for k, want := range map[string]string{
		"f1": "\x1bOP", "f4": "\x1bOS", "f5": "\x1b[15~", "f10": "\x1b[21~", "f11": "\x1b[23~", "f12": "\x1b[24~",
		"f1-kitty": "\x1b[1;1:1P", "f4-kitty": "\x1b[1;1:1S", "f12-kitty": "\x1b[24;1:1~",
		"enter": "\r", "esc": "\x1b", "tab": "\t", "backspace": "\x7f", "ctrl+a": "\x01", "ctrl+]": "\x1d", "ctrl+\\": "\x1c",
	} {
		if got, ok := Key(k); !ok || got != want {
			t.Errorf("%s = %q; want %q", k, got, want)
		}
	}
}

// Reading refuses another version and a line that is no event.
func TestReadRefuses(t *testing.T) {
	for _, bad := range []string{
		`{"v":99,"client":"x"}` + "\n",
		`{"v":1,"client":"x"}` + "\n" + "not json\n",
		`{"v":1,"client":"x"}` + "\n" + `{"t":1}` + "\n", // no kind
		"",
	} {
		if _, err := Read(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// A quiet wait that never comes says so in the recording.
func TestRecordMarksAQuietTimeout(t *testing.T) {
	rec := record(t, "noisy", "quiet 300ms max 600ms\nmark after\n")
	timedOut := false
	for _, e := range rec.Events {
		if e.Kind == KindMark && e.Text == "quiet-timeout" {
			timedOut = true
		}
	}
	if !timedOut {
		t.Fatal("no quiet-timeout mark")
	}
	rec = record(t, "free", "quiet 300ms max 5s\nmark after\ntype q\nquiet 300ms max 5s\n")
	for _, e := range rec.Events {
		if e.Kind == KindMark && e.Text == "quiet-timeout" {
			t.Fatal("a quiet client timed out")
		}
	}
}

// OutputBetween starts at the FIRST mark a and ends at the next b; a
// repeated a does not restart it.
func TestOutputBetween(t *testing.T) {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	rec := Recording{Events: []Event{
		{Kind: KindOutput, Data: b64("before")},
		{Kind: KindMark, Text: "a"}, {Kind: KindOutput, Data: b64("x")},
		{Kind: KindMark, Text: "a"}, {Kind: KindOutput, Data: b64("y")},
		{Kind: KindMark, Text: "b"}, {Kind: KindOutput, Data: b64("after")},
		{Kind: KindMark, Text: "b"},
	}}
	if got, ok := rec.OutputBetween("a", "b"); !ok || string(got) != "xy" {
		t.Fatalf("between = %q %v", got, ok)
	}
	if got, ok := rec.OutputBetween("b", "a"); ok {
		t.Fatalf("b then a = %q", got)
	}
	if got, ok := (Recording{Events: []Event{{Kind: KindMark, Text: "a"}, {Kind: KindMark, Text: "b"}}}).OutputBetween("a", "b"); !ok || got == nil || len(got) != 0 {
		t.Fatalf("nothing between = %q %v", got, ok)
	}
}
