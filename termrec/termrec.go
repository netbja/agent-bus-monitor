// Package termrec records a terminal client under a PTY (herdr exit step 3,
// spec 2026-11-10-herdr-exit-design.md 6.4): the bytes it writes, what was
// typed, the resizes, named marks and the hook lines it sent, each with its
// time. A recording is the raw fixture an emulator replays (step 4) and the
// evidence a profile row cites (4.9). Nothing here judges a screen: without
// an emulator, the only judgement is on bytes (OutputBetween).
package termrec

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// Version is the recording format's; Read refuses another.
const Version = 1

// HookEnv names the unix socket the client's hooks may write lines to; each
// line becomes a hook event.
const HookEnv = "AGENTBUS_TERM_HOOKS"

// The event kinds.
const (
	KindOutput = "out"
	KindInput  = "in"
	KindResize = "resize"
	KindMark   = "mark"
	KindHook   = "hook"
	KindExit   = "exit"
)

// Header is a recording's first line.
type Header struct {
	V             int       `json:"v"`
	Client        string    `json:"client"`
	Fixture       string    `json:"fixture"`
	ClientVersion string    `json:"client_version,omitempty"`
	Argv          []string  `json:"argv"`
	Term          string    `json:"term"`
	Cols          int       `json:"cols"`
	Rows          int       `json:"rows"`
	Start         time.Time `json:"start"`
}

// Event is one line after the header. T: milliseconds since the start. Data:
// the bytes of an output or input, base64. Text: a mark's name, a hook line,
// how the client ended.
type Event struct {
	T    int64  `json:"t"`
	Kind string `json:"k"`
	Data string `json:"d,omitempty"`
	Text string `json:"s,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// Bytes are an output's or an input's bytes.
func (e Event) Bytes() []byte {
	b, _ := base64.StdEncoding.DecodeString(e.Data)
	return b
}

// Recording is a header and its events, in time order.
type Recording struct {
	Header Header
	Events []Event
}

// Output is every byte the client wrote.
func (r Recording) Output() []byte {
	var out []byte
	for _, e := range r.Events {
		if e.Kind == KindOutput {
			out = append(out, e.Bytes()...)
		}
	}
	return out
}

// OutputBetween is what the client wrote between the marks a and b (the
// first of each, b after a); ok=false when either is missing.
func (r Recording) OutputBetween(a, b string) (out []byte, ok bool) {
	in := false
	for _, e := range r.Events {
		switch {
		case e.Kind == KindMark && !in && e.Text == a:
			in, out = true, []byte{}
		case e.Kind == KindMark && in && e.Text == b:
			return out, true
		case in && e.Kind == KindOutput:
			out = append(out, e.Bytes()...)
		}
	}
	return nil, false
}

// Read reads a recording; a line that is no event, or another version, is
// refused.
func Read(r io.Reader) (Recording, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<24)
	var rec Recording
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return rec, err
		}
		return rec, errors.New("empty recording")
	}
	if err := json.Unmarshal(sc.Bytes(), &rec.Header); err != nil {
		return rec, fmt.Errorf("header: %w", err)
	}
	if rec.Header.V != Version {
		return rec, fmt.Errorf("recording version %d, want %d", rec.Header.V, Version)
	}
	for n := 2; sc.Scan(); n++ {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil || e.Kind == "" {
			return rec, fmt.Errorf("line %d: not an event", n)
		}
		rec.Events = append(rec.Events, e)
	}
	return rec, sc.Err()
}

// Step is one line of a script: bytes to type, a sleep, a wait for quiet
// output (Quiet: no output for Wait, at most Max), a mark, or a resize.
type Step struct {
	Bytes      string
	Wait, Max  time.Duration
	Quiet      bool
	Mark       string
	Cols, Rows int
}

// maxWait bounds every wait of a script: a recording is minutes, not hours.
const maxWait = 5 * time.Minute

// ParseScript reads a script, one step a line; # starts a comment.
//
//	type <text>               the text as typed (no Enter: key enter)
//	key <name>                enter, esc, tab, backspace, ctrl+<a-z\]>, f1-f12, f1-kitty…f12-kitty
//	sleep <dur>               wait
//	quiet <dur> [max <dur>]   wait until the client has written, then nothing went either way
//	                          (no output, input or resize) for dur; max 30s by default
//	mark <name>               a named point in the recording
//	resize <cols>x<rows>      resize the terminal
func ParseScript(s string) ([]Step, error) {
	var steps []Step
	dur := func(v string) (time.Duration, error) {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 || d > maxWait {
			return 0, fmt.Errorf("duration %q (above 0, at most %s)", v, maxWait)
		}
		return d, nil
	}
	for n, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		verb, arg, _ := strings.Cut(line, " ")
		arg = strings.TrimSpace(arg)
		bad := func(why string) error { return fmt.Errorf("script line %d %q: %s", n+1, line, why) }
		var st Step
		switch verb {
		case "type":
			if arg == "" {
				return nil, bad("nothing to type")
			}
			st.Bytes = arg
		case "key":
			b, ok := Key(arg)
			if !ok {
				return nil, bad("unknown key")
			}
			st.Bytes = b
		case "sleep":
			d, err := dur(arg)
			if err != nil {
				return nil, bad(err.Error())
			}
			st.Wait = d
		case "quiet":
			f := strings.Fields(arg)
			if len(f) != 1 && (len(f) != 3 || f[1] != "max") {
				return nil, bad("quiet <dur> [max <dur>]")
			}
			d, err := dur(f[0])
			if err != nil {
				return nil, bad(err.Error())
			}
			st.Quiet, st.Wait, st.Max = true, d, 30*time.Second
			if len(f) == 3 {
				if st.Max, err = dur(f[2]); err != nil {
					return nil, bad(err.Error())
				}
				if st.Max < d {
					return nil, bad("max below the quiet time")
				}
			}
		case "mark":
			if arg == "" || strings.ContainsAny(arg, " \t") {
				return nil, bad("mark <name>")
			}
			st.Mark = arg
		case "resize":
			c, r, ok := strings.Cut(arg, "x")
			cols, err1 := strconv.Atoi(c)
			rows, err2 := strconv.Atoi(r)
			if !ok || err1 != nil || err2 != nil || cols < 1 || rows < 1 || cols > 1000 || rows > 1000 {
				return nil, bad("resize <cols>x<rows>")
			}
			st.Cols, st.Rows = cols, rows
		default:
			return nil, bad("unknown step")
		}
		steps = append(steps, st)
	}
	return steps, nil
}

// legacyF are F1-F12 as xterm sends them; kittyF in the kitty keyboard
// protocol with the press event reported (CSI number ; 1:1 final).
var legacyF = [...]string{"\x1bOP", "\x1bOQ", "\x1bOR", "\x1bOS", "\x1b[15~", "\x1b[17~", "\x1b[18~", "\x1b[19~", "\x1b[20~", "\x1b[21~", "\x1b[23~", "\x1b[24~"}

var kittyF = [...]string{"\x1b[1;1:1P", "\x1b[1;1:1Q", "\x1b[1;1:1R", "\x1b[1;1:1S", "\x1b[15;1:1~", "\x1b[17;1:1~", "\x1b[18;1:1~", "\x1b[19;1:1~", "\x1b[20;1:1~", "\x1b[21;1:1~", "\x1b[23;1:1~", "\x1b[24;1:1~"}

// Key is the bytes a named key sends.
func Key(name string) (string, bool) {
	switch name {
	case "enter":
		return "\r", true
	case "esc":
		return "\x1b", true
	case "tab":
		return "\t", true
	case "backspace":
		return "\x7f", true
	}
	if c, ok := strings.CutPrefix(name, "ctrl+"); ok && len(c) == 1 {
		switch b := c[0]; {
		case b >= 'a' && b <= 'z':
			return string(rune(b - 'a' + 1)), true
		case b == '\\' || b == ']':
			return string(rune(b - '@')), true
		}
		return "", false
	}
	f, kitty := strings.CutSuffix(name, "-kitty")
	if n, err := strconv.Atoi(strings.TrimPrefix(f, "f")); strings.HasPrefix(f, "f") && err == nil && n >= 1 && n <= 12 {
		if kitty {
			return kittyF[n-1], true
		}
		return legacyF[n-1], true
	}
	return "", false
}

// Options are one recording's.
type Options struct {
	Client, Fixture, ClientVersion string
	Argv                           []string
	Env                            []string // added to the recorder's environment
	Cols, Rows                     int
	Steps                          []Step
}

// recordTerm is the TERM the client sees, the same on every host.
const recordTerm = "xterm-256color"

// stopGrace is how long a client has to end after the script, then after
// SIGTERM, before the next signal.
const stopGrace = 2 * time.Second

// Record runs the client under a PTY of the given size, plays the steps,
// stops the client if it is still running, and writes the recording to w.
func Record(ctx context.Context, w io.Writer, o Options) error {
	if len(o.Argv) == 0 || o.Cols < 1 || o.Rows < 1 {
		return errors.New("record: a command and a size are needed")
	}
	start := time.Now()
	var (
		mu      sync.Mutex
		enc     = json.NewEncoder(w)
		werr    error
		lastOut = start
		wrote   bool // the client has written something: a quiet wait needs it
	)
	emit := func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		e.T = time.Since(start).Milliseconds()
		if e.Kind == KindOutput || e.Kind == KindInput || e.Kind == KindResize {
			lastOut = time.Now() // quiet counts from what we sent too: an answer (or a redraw) is still coming
		}
		if e.Kind == KindOutput {
			wrote = true
		}
		if werr == nil {
			werr = enc.Encode(e)
		}
	}
	if err := enc.Encode(Header{V: Version, Client: o.Client, Fixture: o.Fixture, ClientVersion: o.ClientVersion,
		Argv: o.Argv, Term: recordTerm, Cols: o.Cols, Rows: o.Rows, Start: start.UTC()}); err != nil {
		return err
	}

	dir, err := os.MkdirTemp("", "termrec-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "hooks.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	var (
		hooks sync.WaitGroup
		conns []net.Conn // closed at the end: a hook writer that outlives the client holds nothing open
	)
	hooks.Add(1)
	go func() {
		defer hooks.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			hooks.Add(1)
			go func() {
				defer hooks.Done()
				defer c.Close()
				sc := bufio.NewScanner(io.LimitReader(c, 1<<20))
				sc.Buffer(make([]byte, 0, 1<<16), 1<<20)
				for sc.Scan() {
					emit(Event{Kind: KindHook, Text: sc.Text()})
				}
			}()
		}
	}()

	cmd := exec.Command(o.Argv[0], o.Argv[1:]...)
	cmd.Env = append(append(os.Environ(), o.Env...), "TERM="+recordTerm, HookEnv+"="+sock)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(o.Cols), Rows: uint16(o.Rows)})
	if err != nil {
		ln.Close()
		hooks.Wait()
		return err
	}
	read := make(chan struct{})
	go func() {
		defer close(read)
		buf := make([]byte, 32<<10)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				emit(Event{Kind: KindOutput, Data: base64.StdEncoding.EncodeToString(buf[:n])})
			}
			if err != nil {
				return
			}
		}
	}()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	sleep := func(d time.Duration) bool {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
			return true
		}
	}
	quiet := func(d time.Duration) bool { // the client wrote, then nothing came either way for d
		mu.Lock()
		defer mu.Unlock()
		return wrote && time.Since(lastOut) >= d
	}
	for _, st := range o.Steps {
		if ctx.Err() != nil {
			break
		}
		switch {
		case st.Bytes != "":
			if _, err := io.WriteString(f, st.Bytes); err != nil {
				emit(Event{Kind: KindMark, Text: "write failed: " + err.Error()})
				break
			}
			emit(Event{Kind: KindInput, Data: base64.StdEncoding.EncodeToString([]byte(st.Bytes))})
		case st.Quiet:
			deadline := time.Now().Add(st.Max)
			for !quiet(st.Wait) && time.Now().Before(deadline) && sleep(20*time.Millisecond) {
			}
			if !quiet(st.Wait) {
				emit(Event{Kind: KindMark, Text: "quiet-timeout"})
			}
		case st.Wait > 0:
			sleep(st.Wait)
		case st.Mark != "":
			emit(Event{Kind: KindMark, Text: st.Mark})
		case st.Cols > 0:
			if err := pty.Setsize(f, &pty.Winsize{Cols: uint16(st.Cols), Rows: uint16(st.Rows)}); err == nil {
				emit(Event{Kind: KindResize, Cols: st.Cols, Rows: st.Rows})
			}
		}
	}

	how := "exited"
	var werrExit error
	select {
	case werrExit = <-exited:
	case <-time.After(stopGrace):
		how = "stopped"
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case werrExit = <-exited:
		case <-time.After(stopGrace):
			_ = cmd.Process.Kill()
			werrExit = <-exited
		}
	}
	select { // the output written before the end, then the end
	case <-read:
	case <-time.After(stopGrace):
	}
	f.Close()
	<-read
	ln.Close()
	mu.Lock()
	for _, c := range conns {
		c.Close()
	}
	mu.Unlock()
	hooks.Wait()
	status := "0"
	if werrExit != nil {
		status = werrExit.Error()
	}
	emit(Event{Kind: KindExit, Text: how + ": " + status})
	mu.Lock()
	defer mu.Unlock()
	return werr
}
