package holder

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/term"

	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/service"
)

// The test binary doubles as the programs a terminal runs (never a real
// agent, spec 8): HOLDER_TEST_MODE picks one, before the tests run.
func TestMain(m *testing.M) {
	switch os.Getenv("HOLDER_TEST_MODE") {
	case "client": // a process inside a terminal driving its own holder (security S2b)
		insideClient()
		return
	case "dsr": // asks the terminal where its cursor is, as a client's startup does
		askCursor()
		return
	}
	os.Exit(m.Run())
}

func insideClient() {
	cl, err := Dial(os.Getenv("AGENTBUS_HOST"), "test")
	if err != nil {
		fmt.Printf("RESULT dial=%v\n", err)
		time.Sleep(30 * time.Second)
		return
	}
	self := os.Getenv("AGENTBUS_TERM_ID")
	code := func(req any) string {
		_, err := cl.Call(req)
		var re *RemoteError
		switch {
		case err == nil:
			return "ok"
		case errors.As(err, &re):
			return re.Code
		}
		return "error"
	}
	fmt.Printf("RESULT start=%s close=%s submit=%s type=%s observe=%s screen=%s list=%s end\n",
		code(Start{Role: "echoer"}), code(Close{Term: self}), code(Submit{Kind: "clear", Term: self}),
		code(Attach{Term: self}), code(Attach{Term: self, Observe: true}), code(ScreenReq{Term: self}), code(List{}))
	time.Sleep(30 * time.Second)
}

func askCursor() {
	old, err := term.MakeRaw(0)
	if err != nil {
		fmt.Println("no raw:", err)
		return
	}
	os.Stdout.WriteString("\x1b[3;5H\x1b[6n")
	var got []byte
	b := make([]byte, 1)
	for len(got) < 32 {
		if n, err := os.Stdin.Read(b); err != nil || n == 0 {
			break
		}
		got = append(got, b[0])
		if b[0] == 'R' {
			break
		}
	}
	term.Restore(0, old)
	fmt.Printf("\r\ngot:%q\r\n", strings.TrimPrefix(string(got), "\x1b"))
	time.Sleep(30 * time.Second)
}

// testRoles are the scripted programs, by role: each a command the test
// launch returns (the production launch is TemplateLaunch).
func testRoles(t *testing.T) func(Start) (Command, error) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sh := func(script string) Command { return Command{Argv: []string{"/bin/sh", "-c", script}} }
	return func(s Start) (Command, error) {
		switch s.Role {
		case "echoer":
			return sh(`printf 'hello %s\n' "$AGENTBUS_TERM_ID"; printf '\033[?2004h'; exec sleep 30`), nil
		case "exiter":
			return sh(`printf 'bye\n'; sleep 0.2; exit 7`), nil
		case "insider":
			return Command{Argv: []string{self, "-test.run=^$"}, Env: []string{"HOLDER_TEST_MODE=client"}}, nil
		case "forker": // its client is a double-forked grandchild, orphaned at once
			return sh(`( HOLDER_TEST_MODE=client "` + self + `" -test.run='^$' & ) ; exec sleep 30`), nil
		case "leaver": // its shell exits, a child that ignores SIGHUP stays in its process group
			return sh(`trap '' HUP; sleep 60 & echo "CHILD $!"; exit 0`), nil
		case "orphaner":
			return sh(`( sleep 1.5 & ) ; exec sleep 30`), nil
		case "tree":
			return sh(`sleep 31 & sleep 32 & wait`), nil
		case "dsr":
			return Command{Argv: []string{self, "-test.run=^$"}, Env: []string{"HOLDER_TEST_MODE=dsr"}}, nil
		}
		return Command{}, fmt.Errorf("role %s is not in the manifest", s.Role)
	}
}

// newTestHolder serves a holder for project demo in a fresh run directory.
func newTestHolder(t *testing.T) (*Server, string) {
	t.Helper()
	return newTestHolderWith(t, func(*Config) {})
}

func newTestHolderWith(t *testing.T, set func(*Config)) (*Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "hx") // short: a socket path holds 103 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	os.Chmod(dir, 0o700)
	sock, err := SocketPath(func(k string) string {
		if k == "XDG_RUNTIME_DIR" {
			return dir
		}
		return ""
	}, "demo", service.BuildKey("test"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Project: "demo", Build: "test", Socket: sock, Launch: testRoles(t), Grace: 2 * time.Second}
	set(&cfg)
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.Serve(ctx)
	t.Cleanup(func() { cancel(); s.Shutdown() })
	return s, sock
}

func dial(t *testing.T, sock string) *Client {
	t.Helper()
	cl, err := Dial(sock, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })
	return cl
}

func startRole(t *testing.T, cl *Client, role string) TermInfo {
	t.Helper()
	r, err := cl.Call(Start{Role: role, Cols: 100, Rows: 30})
	if err != nil || r.Term == nil {
		t.Fatalf("start %s: %+v %v", role, r, err)
	}
	return *r.Term
}

// waitScreen polls the screen until it holds want.
func waitScreen(t *testing.T, cl *Client, id, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var text string
	for time.Now().Before(deadline) {
		r, err := cl.Call(ScreenReq{Term: id})
		if err == nil && r.Screen != nil {
			text = strings.Join(r.Screen.Lines, "\n")
			if strings.Contains(text, want) {
				return text
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("screen of %s never showed %q:\n%s", id, want, text)
	return ""
}

func termOf(t *testing.T, cl *Client, id string) TermInfo {
	t.Helper()
	r, err := cl.Call(List{})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range r.Terms {
		if x.ID == id {
			return x
		}
	}
	t.Fatalf("no %s in %+v", id, r.Terms)
	return TermInfo{}
}

// Start, Screen, Activity, List and Close on a real PTY (spec 4.2, 4.4):
// the program is the holder's child, sees AGENTBUS_TERM_ID and
// AGENTBUS_HOST, its output reaches the emulator with no viewer, its pid
// and start time are reported, and Close ends it.
func TestHolderStartScreenClose(t *testing.T) {
	s, sock := newTestHolder(t)
	if st, err := os.Lstat(sock); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v %v", st.Mode(), err)
	}
	cl := dial(t, sock)
	if cl.Welcome.Proto != Proto || cl.Welcome.Build != "test" {
		t.Fatalf("welcome %+v", cl.Welcome)
	}
	a := startRole(t, cl, "echoer")
	if a.ID != "t1" || a.Role != "echoer" || !a.Alive || a.PID <= 0 || a.Build != "test" {
		t.Fatalf("started %+v", a)
	}
	if st, err := dispatch.ProcStart(a.PID); err != nil || st != a.PIDStart {
		t.Fatalf("pid start %d, proc says %d %v", a.PIDStart, st, err)
	}
	if ppid := parentOf(a.PID); ppid != os.Getpid() {
		t.Fatalf("the program's parent is %d, not the holder %d", ppid, os.Getpid())
	}
	waitScreen(t, cl, a.ID, "hello t1")
	r, _ := cl.Call(ScreenReq{Term: a.ID})
	if !r.Screen.BracketedPaste {
		t.Fatalf("mode 2004 not seen: %+v", r.Screen)
	}
	if r, err := cl.Call(ActivityReq{Term: a.ID}); err != nil || r.Activity.OutputAt <= 0 {
		t.Fatalf("activity %+v %v", r.Activity, err)
	}
	if r, err := cl.Call(Describe{}); err != nil || r.Info.Project != "demo" || r.Info.Terms != 1 || r.Info.Socket != sock {
		t.Fatalf("describe %+v %v", r.Info, err)
	}
	if _, err := cl.Call(Ping{}); err != nil {
		t.Fatal(err)
	}
	b := startRole(t, cl, "echoer") // never reuses a terminal
	if b.ID == a.ID || b.PID == a.PID {
		t.Fatalf("a second start reused %+v", b)
	}
	r, err := cl.Call(Close{Term: a.ID})
	if err != nil || r.Term.Alive || r.Term.Exit == nil {
		t.Fatalf("close %+v %v", r.Term, err)
	}
	if x := termOf(t, cl, a.ID); x.Alive {
		t.Fatalf("listed alive after close: %+v", x)
	}
	if !termOf(t, cl, b.ID).Alive {
		t.Fatal("closing one closed the other")
	}
	var re *RemoteError
	if _, err := cl.Call(Close{Term: "t99"}); !errors.As(err, &re) || re.Code != CodeUnknown {
		t.Fatalf("an unknown term: %v", err)
	}
	if _, err := cl.Call(Start{Role: "nobody"}); !errors.As(err, &re) || re.Code != CodeRefused {
		t.Fatalf("a role the launch refuses: %v", err)
	}
	_ = s
}

func parentOf(pid int) int {
	st, ok := readProcStat(pid)
	if !ok {
		return 0
	}
	return st.ppid
}

// The program's own exit status is observed by its Wait, the subreaper
// beside it notwithstanding (spec 8, subreaper).
func TestHolderObservesTheExitStatus(t *testing.T) {
	_, sock := newTestHolder(t)
	cl := dial(t, sock)
	o := startRole(t, cl, "orphaner") // the reaper busy beside it
	e := startRole(t, cl, "exiter")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if x := termOf(t, cl, e.ID); !x.Alive {
			if x.Exit == nil || *x.Exit != 7 {
				t.Fatalf("exit %+v", x.Exit)
			}
			if !termOf(t, cl, o.ID).Alive {
				t.Fatal("the orphaner's own program ended")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the exit was never observed")
}

// A double-forked orphan inside a terminal stays the holder's (subreaper)
// and is reaped when it exits: no zombie is left. The orphan lives 1.5 s:
// long enough to be seen adopted, however slow the first read.
func TestHolderReapsOrphans(t *testing.T) {
	_, sock := newTestHolder(t)
	cl := dial(t, sock)
	o := startRole(t, cl, "orphaner")
	orphan := 0
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && orphan == 0 {
		for _, pid := range childrenOf(os.Getpid()) {
			if st, ok := readProcStat(pid); ok && pid != o.PID && st.comm == "sleep" {
				orphan = pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if orphan == 0 {
		t.Fatalf("the orphan was never adopted: children %v", childrenOf(os.Getpid()))
	}
	for time.Now().Before(deadline) {
		if _, ok := readProcStat(orphan); !ok { // reaped: gone from /proc, not a zombie
			if !termOf(t, cl, o.ID).Alive {
				t.Fatal("the terminal's own program ended")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	st, _ := readProcStat(orphan)
	t.Fatalf("orphan %d not reaped (state %c)", orphan, st.state)
}

// Security S2b: a process inside a terminal (and its double-forked
// grandchild) may read but is refused Start, Close, Submit and an attach
// that types; an observer attach is not refused for being in-tree. From
// outside, the same calls are not refused as in-tree.
func TestHolderRefusesInTree(t *testing.T) {
	_, sock := newTestHolder(t)
	cl := dial(t, sock)
	for _, role := range []string{"insider", "forker"} {
		x := startRole(t, cl, role)
		text := waitScreen(t, cl, x.ID, " end")
		want := "RESULT start=in_tree close=in_tree submit=in_tree type=in_tree observe=later screen=ok list=ok end"
		if !strings.Contains(strings.ReplaceAll(text, "\n", ""), want) {
			t.Fatalf("%s:\n%s\nwant %q", role, text, want)
		}
	}
	e := startRole(t, cl, "echoer")
	var re *RemoteError
	for _, req := range []any{Submit{Kind: "clear", Term: e.ID}, Attach{Term: e.ID}} {
		if _, err := cl.Call(req); !errors.As(err, &re) || re.Code != CodeLater {
			t.Fatalf("%+v from outside: %v", req, err)
		}
	}
	if terms := termOf(t, cl, e.ID); !terms.Alive {
		t.Fatal("the in-tree close ended a terminal")
	}
}

// The terminal's process tree (spec 4.3b): its program and descendants.
func TestHolderProcesses(t *testing.T) {
	_, sock := newTestHolder(t)
	cl := dial(t, sock)
	x := startRole(t, cl, "tree")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r, err := cl.Call(Processes{Term: x.ID})
		if err != nil {
			t.Fatal(err)
		}
		sleeps := 0
		for _, p := range r.Procs {
			if p.Command == "sleep" && p.PPID == x.PID && p.Start > 0 {
				sleeps++
			}
		}
		if len(r.Procs) >= 3 && r.Procs[0].PID == x.PID && sleeps == 2 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the tree was never read")
}

// The holder answers the program's queries itself, no viewer attached
// (spec 4.6, S3): a cursor position request gets its report.
func TestHolderAnswersQueries(t *testing.T) {
	_, sock := newTestHolder(t)
	cl := dial(t, sock)
	x := startRole(t, cl, "dsr")
	waitScreen(t, cl, x.ID, `got:"[3;5R"`)
}

// The handshake: hello first, in the holder's protocol; any other first
// frame or protocol is refused and the connection closed. A client with no
// hello gets nothing done.
func TestHolderHandshake(t *testing.T) {
	_, sock := newTestHolder(t)
	raw := func(t *testing.T, first any) string {
		c, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		b, _ := EncodeControl(first)
		WriteFrame(c, FrameControl, b)
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, body, err := ReadFrame(c)
		if err != nil {
			return "closed"
		}
		if _, _, err := ReadFrame(c); err == nil {
			t.Fatal("the connection stayed open after a refused hello")
		}
		return string(body)
	}
	if got := raw(t, Hello{Proto: Proto + 1, Build: "x"}); !strings.Contains(got, "protocol 2 is not served") {
		t.Fatalf("another protocol: %s", got)
	}
	if got := raw(t, Start{Role: "echoer"}); !strings.Contains(got, "the first frame is hello") {
		t.Fatalf("no hello: %s", got)
	}
	// After the welcome: a frame outside the closed set is refused, the
	// connection kept.
	cl := dial(t, sock)
	if err := WriteFrame(cl.c, FrameControl, []byte(`{"op":"start","role":"echoer","argv":["sh"]}`)); err != nil {
		t.Fatal(err)
	}
	_, body, err := ReadFrame(cl.c)
	if err != nil || !strings.Contains(string(body), CodeBadFrame) || !strings.Contains(string(body), `takes no field \"argv\"`) {
		t.Fatalf("an argv field: %s %v", body, err)
	}
	if r, err := cl.Call(List{}); err != nil || len(r.Terms) != 0 {
		t.Fatalf("the refused start started something: %+v %v", r, err)
	}
	if err := WriteFrame(cl.c, FrameData, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, body, _ := ReadFrame(cl.c); !strings.Contains(string(body), "a data frame outside an attach") {
		t.Fatalf("a data frame: %s", body)
	}
}

// Another uid is refused before any frame is read (security S6): the
// connection closes with no welcome.
func TestHolderRefusesAnotherUser(t *testing.T) {
	_, sock := newTestHolderWith(t, func(c *Config) {
		c.peerOf = func(c *net.UnixConn) (Peer, error) {
			p, err := PeerOf(c)
			p.UID++
			return p, err
		}
	})
	if _, err := Dial(sock, "test"); err == nil {
		t.Fatal("a peer of another uid was welcomed")
	}
}

// Close sends SIGHUP first: a program that ends on it ends at once, well
// before the grace's SIGKILL.
func TestHolderCloseHangsUpFirst(t *testing.T) {
	_, sock := newTestHolder(t) // grace 2 s
	cl := dial(t, sock)
	x := startRole(t, cl, "echoer")
	waitScreen(t, cl, x.ID, "hello")
	began := time.Now()
	if _, err := cl.Call(Close{Term: x.ID}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(began); d > time.Second {
		t.Fatalf("close took %v: no SIGHUP before the grace's SIGKILL", d)
	}
}

// Close ends the terminal's whole process group, its leader gone or not
// (reviewer-codex on 5b): a child that ignores SIGHUP and outlived its
// shell is killed after the grace, never left running.
func TestHolderCloseEndsAnExitedLeadersGroup(t *testing.T) {
	_, sock := newTestHolder(t) // grace 2 s
	cl := dial(t, sock)
	x := startRole(t, cl, "leaver")
	text := waitScreen(t, cl, x.ID, "CHILD ")
	var child int
	for _, l := range strings.Split(text, "\n") {
		if n, ok := strings.CutPrefix(strings.TrimSpace(l), "CHILD "); ok {
			child, _ = strconv.Atoi(n)
		}
	}
	if child <= 0 {
		t.Fatalf("no child pid in %q", text)
	}
	eventually := func(what string, cond func() bool) {
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if cond() {
				return
			}
		}
		t.Fatalf("never: %s", what)
	}
	eventually("the leader exited", func() bool { return !termOf(t, cl, x.ID).Alive })
	if st, ok := readProcStat(child); !ok || st.state == 'Z' {
		t.Fatalf("the child is not running before the close: %+v %v", st, ok)
	}
	if _, err := cl.Call(Close{Term: x.ID}); err != nil {
		t.Fatal(err)
	}
	eventually("the child ended", func() bool {
		st, ok := readProcStat(child)
		return !ok || st.state == 'Z'
	})
}

// Listen: a socket that answers is another holder (refused); a stale one is
// replaced.
func TestHolderListen(t *testing.T) {
	s, sock := newTestHolder(t)
	other, err := NewServer(Config{Project: "demo", Build: "test", Socket: sock, Launch: testRoles(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Listen(); err == nil || !strings.Contains(err.Error(), "already answers") {
		t.Fatalf("a live socket: %v", err)
	}
	s.Shutdown()
	stale := filepath.Join(filepath.Dir(sock), "holder-stale.0123456789.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: stale, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	l.Close()
	third, _ := NewServer(Config{Project: "demo", Build: "test", Socket: stale, Launch: testRoles(t)})
	if err := third.Listen(); err != nil {
		t.Fatalf("a stale socket: %v", err)
	}
	third.Shutdown()
	if _, err := NewServer(Config{Project: "Bad", Build: "test", Socket: stale, Launch: testRoles(t)}); err == nil {
		t.Fatal("an invalid project")
	}
}
