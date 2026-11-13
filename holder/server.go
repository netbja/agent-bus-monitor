package holder

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/screen"
)

// Config is a holder's: one project (spec 4.4, Q4), its build, its socket
// (SocketPath), how a Start frame becomes a command (TemplateLaunch.Command
// in production), the screens' scrollback and the close grace.
type Config struct {
	Project    string
	Build      string
	Socket     string
	Launch     func(Start) (Command, error)
	Scrollback int           // holder.scrollback, default 5000
	Grace      time.Duration // SIGHUP to SIGKILL, default 5 s
	OSC52      bool          // attach.osc52: pass OSC 52 to viewers (default off, spec 4.6)

	// ancestors is dispatch.Ancestors; procStart dispatch.ProcStart (tests
	// replace them).
	ancestors func(int) []dispatch.ProcID
	procStart func(int) (uint64, error)
	peerOf    func(*net.UnixConn) (Peer, error)
	kill      func(pid int, sig syscall.Signal) error // nil: syscall.Kill (tests record the signals)
	others    func(pgid int) bool                     // nil: othersInGroup (a test makes the scan miss)
	escWait   time.Duration                           // 0: escWait (a test sets it long)
}

// Defaults.
const (
	DefaultScrollback = 5000
	DefaultGrace      = 5 * time.Second
	defaultCols       = 120
	defaultRows       = 40
)

// Server is a holder: it owns its terminals' PTYs and is their parent.
type Server struct {
	cfg  Config
	self dispatch.ProcID

	mu    sync.Mutex
	terms map[string]*terminal
	order []string
	next  int
	ln    *net.UnixListener
	stop  chan struct{}
	conns map[*net.UnixConn]bool
}

// NewServer checks cfg and becomes a subreaper (Linux).
func NewServer(cfg Config) (*Server, error) {
	if !bus.ValidName(cfg.Project) {
		return nil, fmt.Errorf("holder: invalid project %q", cfg.Project)
	}
	if err := validBuild(cfg.Build); err != nil {
		return nil, err
	}
	if cfg.Launch == nil || cfg.Socket == "" {
		return nil, errors.New("holder: a launch and a socket are required")
	}
	if cfg.Scrollback <= 0 {
		cfg.Scrollback = DefaultScrollback
	}
	if cfg.escWait <= 0 {
		cfg.escWait = escWait
	}
	if cfg.Grace <= 0 {
		cfg.Grace = DefaultGrace
	}
	if cfg.ancestors == nil {
		cfg.ancestors = dispatch.Ancestors
	}
	if cfg.procStart == nil {
		cfg.procStart = dispatch.ProcStart
	}
	if cfg.peerOf == nil {
		cfg.peerOf = PeerOf
	}
	start, err := cfg.procStart(os.Getpid())
	if err != nil {
		return nil, fmt.Errorf("holder: its own start time: %w", err)
	}
	if err := becomeSubreaper(); err != nil {
		return nil, fmt.Errorf("holder: subreaper: %w", err)
	}
	return &Server{cfg: cfg, self: dispatch.ProcID{PID: os.Getpid(), Start: start},
		terms: map[string]*terminal{}, stop: make(chan struct{}), conns: map[*net.UnixConn]bool{}}, nil
}

// Listen opens the socket, mode 0600. A socket that still answers is
// another holder of this build: refused. A stale one is removed.
func (s *Server) Listen() error {
	if c, err := net.DialTimeout("unix", s.cfg.Socket, time.Second); err == nil {
		c.Close()
		return fmt.Errorf("holder: %s already answers: another holder of this build runs", s.cfg.Socket)
	}
	_ = os.Remove(s.cfg.Socket)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: s.cfg.Socket, Net: "unix"})
	if err != nil {
		return err
	}
	if err := os.Chmod(s.cfg.Socket, 0o600); err != nil {
		ln.Close()
		return err
	}
	s.ln = ln
	startReaper()
	return nil
}

// The subreaper is the process's, not one server's: every terminal program
// of every server in the process is tracked in one registry, and one reaper
// reads it (a server's reaper knowing only its own terminals would reap
// another's program and take its exit status).
var (
	trackMu    sync.Mutex
	trackedSet = map[int]bool{}
	reaperOnce sync.Once
)

func track(pid int, on bool) {
	trackMu.Lock()
	defer trackMu.Unlock()
	if on {
		trackedSet[pid] = true
	} else {
		delete(trackedSet, pid)
	}
}

// tracked: pid is a terminal's program (its own Wait reaps it).
func tracked(pid int) bool {
	trackMu.Lock()
	defer trackMu.Unlock()
	return trackedSet[pid]
}

func startReaper() { reaperOnce.Do(func() { go reapLoop(nil, tracked) }) }

// Serve accepts connections until ctx ends or Shutdown.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		select {
		case <-ctx.Done():
			s.ln.Close()
		case <-s.stop:
		}
	}()
	for {
		c, err := s.ln.AcceptUnix()
		if err != nil {
			select {
			case <-s.stop:
				return nil
			default:
			}
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.serveConn(c)
	}
}

// Shutdown stops listening and closes every terminal (tests; a holder in
// service exits only when its last terminal ended, spec 4.4).
func (s *Server) Shutdown() {
	s.mu.Lock()
	select {
	case <-s.stop:
		s.mu.Unlock()
		return
	default:
	}
	close(s.stop)
	if s.ln != nil {
		s.ln.Close()
	}
	for c := range s.conns {
		c.Close()
	}
	terms := make([]*terminal, 0, len(s.terms))
	for _, t := range s.terms {
		terms = append(terms, t)
	}
	s.mu.Unlock()
	for _, t := range terms {
		t.close(s.cfg.Grace)
	}
	_ = os.Remove(s.cfg.Socket)
}

// serveConn is one client: same uid only (S6), then hello and welcome,
// then one reply per control frame. A peer inside a terminal (in-tree,
// S2b) may read but never start, close, submit or attach to type.
func (s *Server) serveConn(c *net.UnixConn) {
	defer c.Close()
	s.mu.Lock()
	s.conns[c] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
	}()
	peer, err := s.cfg.peerOf(c)
	if err != nil || CheckSameUser(peer) != nil {
		return
	}
	inTree := InTree(peer.PID, s.self, s.cfg.ancestors)
	typ, body, err := ReadFrame(c)
	if err != nil || typ != FrameControl {
		return
	}
	v, err := DecodeControl(body)
	hello, ok := v.(Hello)
	if err != nil || !ok {
		_ = WriteFrame(c, FrameControl, errReply(CodeBadFrame, "the first frame is hello").encode())
		return
	}
	if hello.Proto != Proto {
		_ = WriteFrame(c, FrameControl, errReply(CodeBadFrame, "protocol %d is not served (this holder speaks %d)", hello.Proto, Proto).encode())
		return
	}
	w, _ := EncodeControl(Welcome{Proto: Proto, Build: s.cfg.Build})
	if err := WriteFrame(c, FrameControl, w); err != nil {
		return
	}
	for {
		typ, body, err := ReadFrame(c)
		if err != nil {
			return
		}
		var r Reply
		if typ != FrameControl {
			r = errReply(CodeBadFrame, "a data frame outside an attach")
		} else if v, err := DecodeControl(body); err != nil {
			r = errReply(CodeBadFrame, "%v", err)
		} else if a, ok := v.(Attach); ok && (!inTree || a.Observe) {
			t, found := s.term(a.Term)
			if !found {
				r = errReply(CodeUnknown, "no terminal %s", a.Term)
			} else {
				s.attach(c, t, a) // the connection is the viewer's until it ends
				return
			}
		} else {
			r = s.handle(v, inTree)
		}
		if err := WriteFrame(c, FrameControl, r.encode()); err != nil {
			return
		}
	}
}

// handle answers one decoded frame.
func (s *Server) handle(v any, inTree bool) Reply {
	refuseInTree := func(what string) Reply {
		return errReply(CodeInTree, "%s refused from inside an agent's terminal: run it from your own shell, or through the dispatcher", what)
	}
	switch f := v.(type) {
	case Ping:
		return Reply{}
	case Describe:
		s.mu.Lock()
		n := len(s.terms)
		s.mu.Unlock()
		return Reply{Info: &Info{Project: s.cfg.Project, Build: s.cfg.Build, Proto: Proto, Terms: n, Socket: s.cfg.Socket}}
	case List:
		s.mu.Lock()
		terms := make([]*terminal, 0, len(s.order))
		for _, id := range s.order {
			terms = append(terms, s.terms[id])
		}
		s.mu.Unlock()
		out := make([]TermInfo, 0, len(terms))
		for _, t := range terms {
			out = append(out, t.info(s.cfg.Build))
		}
		return Reply{Terms: out}
	case Start:
		if inTree {
			return refuseInTree("start")
		}
		return s.start(f)
	case Close:
		if inTree {
			return refuseInTree("close")
		}
		t, ok := s.term(f.Term)
		if !ok {
			return errReply(CodeUnknown, "no terminal %s", f.Term)
		}
		t.close(s.cfg.Grace)
		info := t.info(s.cfg.Build)
		return Reply{Term: &info}
	case ScreenReq:
		t, ok := s.term(f.Term)
		if !ok {
			return errReply(CodeUnknown, "no terminal %s", f.Term)
		}
		return Reply{Screen: t.screenInfo(f.Lines)}
	case ActivityReq:
		t, ok := s.term(f.Term)
		if !ok {
			return errReply(CodeUnknown, "no terminal %s", f.Term)
		}
		return Reply{Activity: t.activity()}
	case Processes:
		t, ok := s.term(f.Term)
		if !ok {
			return errReply(CodeUnknown, "no terminal %s", f.Term)
		}
		if !t.info(s.cfg.Build).Alive {
			return Reply{}
		}
		return Reply{Procs: processTree(t.pid)}
	case Submit:
		if inTree {
			return refuseInTree("submit")
		}
		return errReply(CodeLater, "submit is not served yet")
	case Attach: // served by serveConn; here only when refused
		return refuseInTree("an attach that types")
	case Focus, Resize, Detach:
		return errReply(CodeBadFrame, "not attached")
	}
	return errReply(CodeBadFrame, "%T is not a request", v)
}

func (s *Server) term(id string) (*terminal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.terms[id]
	return t, ok
}

// start runs a role's launch on a new PTY (never reusing a terminal). The
// lock is held from the fork until the pid is recorded, so the reaper never
// takes a new terminal's program for an orphan.
func (s *Server) start(f Start) Reply {
	cmd, err := s.cfg.Launch(f)
	if err != nil {
		return errReply(CodeRefused, "%v", err)
	}
	if len(cmd.Argv) == 0 {
		return errReply(CodeRefused, "no command for role %s", f.Role)
	}
	cols, rows := f.Cols, f.Rows
	if cols == 0 {
		cols, rows = defaultCols, defaultRows
	}
	s.mu.Lock()
	select {
	case <-s.stop:
		s.mu.Unlock()
		return errReply(CodeDraining, "the holder is stopping")
	default:
	}
	s.next++
	id := "t" + strconv.Itoa(s.next)
	c := exec.Command(cmd.Argv[0], cmd.Argv[1:]...)
	c.Dir = cmd.Dir
	c.Env = append(append(os.Environ(), cmd.Env...), "AGENTBUS_TERM_ID="+id, "AGENTBUS_HOST="+s.cfg.Socket)
	// The registry's lock is held from the fork until the pid is tracked,
	// so the reaper never takes the new program for an orphan.
	trackMu.Lock()
	f2, err := pty.StartWithSize(c, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		trackMu.Unlock()
		s.mu.Unlock()
		return errReply(CodeFailed, "start %s: %v", f.Role, err)
	}
	trackedSet[c.Process.Pid] = true
	trackMu.Unlock()
	t := &terminal{id: id, role: f.Role, pid: c.Process.Pid, cmd: c, pty: f2, alive: true, cols: cols, rows: rows, grace: s.cfg.Grace, kill: s.cfg.kill, others: s.cfg.others, escWait: s.cfg.escWait,
		scr: screen.New(cols, rows, s.cfg.Scrollback), done: make(chan struct{}), outDone: make(chan struct{})}
	t.start, _ = s.cfg.procStart(t.pid)
	s.terms[id] = t
	s.order = append(s.order, id)
	s.mu.Unlock()
	go t.readLoop()
	go t.replyLoop()
	go t.waitLoop()
	info := t.info(s.cfg.Build)
	return Reply{Term: &info}
}

// screenInfo is the screen, with up to lines of scrollback above it.
func (t *terminal) screenInfo(lines int) *ScreenInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := t.scr.Snapshot(lines)
	m := t.scr.Modes()
	plain := snap.Plain()
	var out []string
	if plain != "" {
		out = splitLines(plain)
	}
	return &ScreenInfo{Lines: out, Styled: snap.ANSI(), CursorRow: snap.CursorY, CursorCol: snap.CursorX,
		BracketedPaste: m.BracketedPaste, AltScreen: m.AltScreen}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
