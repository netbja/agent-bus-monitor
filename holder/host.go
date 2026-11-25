package holder

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/dispatch"
	"github.com/netbja/agent-bus-monitor/service"
	"github.com/netbja/agent-bus-monitor/termhost"
)

// Host is the holder's adapter of termhost.Host (spec 4.2; herdr exit step
// 6a): the client side of a project's holder sockets. New terminals go to
// the current build's holder; the terminals of an older build that drains
// stay reachable through its own socket, and a TermID names both
// ("<build key>/<terminal id>"). Every call reports what a holder said.
type Host struct {
	project string
	build   string // this binary's raw build id: the current holder's
	getenv  func(string) string
}

// NewHost is the adapter for project's holders, the current one being
// build's (BuildID() in production).
func NewHost(project, build string, getenv func(string) string) (*Host, error) {
	if !bus.ValidName(project) {
		return nil, termhost.ErrInvalidName
	}
	return &Host{project: project, build: build, getenv: getenv}, nil
}

var _ termhost.Host = (*Host)(nil)

func (h *Host) key() string { return service.BuildKey(h.build) }

// sockets is every holder socket of the project, by build key, the current
// one first.
func (h *Host) sockets() (map[string]string, []string, error) {
	dir, err := ProjectDir(h.getenv, h.project)
	if err != nil {
		return nil, nil, err
	}
	socks, err := ProjectSockets(dir) // both names: an older build's holder keeps its legacy one (P0-2 B)
	if err != nil {
		return nil, nil, err
	}
	keys := make([]string, 0, len(socks))
	for key := range socks {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b string) int {
		switch {
		case a == h.key():
			return -1
		case b == h.key():
			return 1
		}
		return strings.Compare(a, b)
	})
	return socks, keys, nil
}

// SocketFiles is every holder socket of the project on disk, answering or
// not (Describe lists the ones that answer).
func (h *Host) SocketFiles() ([]string, error) {
	socks, keys, err := h.sockets()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, socks[k])
	}
	return out, nil
}

// call dials key's holder, sends req under ctx's deadline and returns the reply.
func (h *Host) call(ctx context.Context, key string, req any) (Reply, error) {
	if err := ctx.Err(); err != nil {
		return Reply{}, err
	}
	sock, err := SocketFor(h.getenv, h.project, key)
	if err != nil {
		return Reply{}, err
	}
	cl, err := DialContext(ctx, sock, h.build)
	if err != nil {
		return Reply{}, err
	}
	defer cl.Close()
	if d, ok := ctx.Deadline(); ok {
		_ = cl.c.SetDeadline(d)
	}
	stop := context.AfterFunc(ctx, func() { _ = cl.c.SetDeadline(time.Now()) })
	defer stop()
	r, err := cl.Call(req)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return Reply{}, cerr
		}
		return Reply{}, mapErr(err)
	}
	return r, nil
}

// mapErr turns a holder's error code into termhost's error.
func mapErr(err error) error {
	var re *RemoteError
	if !errors.As(err, &re) {
		return err
	}
	var to error
	switch re.Code {
	case CodeUnknown:
		to = termhost.ErrUnknownTerm
	case CodePersonActive:
		to = termhost.ErrPersonActive
	case CodeNotReady:
		to = termhost.ErrNotReady
	case CodeNotLanded:
		to = termhost.ErrNotLanded
	case CodeLeftBehind:
		to = termhost.ErrLeftBehind
	case CodeScreen:
		to = termhost.ErrScreenUntrusted
	default:
		return err
	}
	return fmt.Errorf("%w (%s)", to, re.Msg)
}

// split reads a TermID: its build key and the holder's terminal id.
func split(id termhost.TermID) (key, term string, err error) {
	key, term, ok := strings.Cut(string(id), "/")
	if !ok || !service.ValidBuildKey(key) || term == "" {
		return "", "", termhost.ErrUnknownTerm
	}
	return key, term, nil
}

func (h *Host) term(key string, t TermInfo) termhost.Term {
	return termhost.Term{ID: termhost.TermID(key + "/" + t.ID), Project: h.project, Name: t.Role, PID: t.PID,
		PIDStart: t.PIDStart, Alive: t.Alive, Viewers: t.Viewers, Build: t.Build, Profile: t.Profile}
}

// Start runs spec on the current build's holder: the role, its profile and
// resume session, the size; never a command (S2a).
func (h *Host) Start(ctx context.Context, s termhost.Spec) (termhost.Term, error) {
	if !bus.ValidName(s.Project) || !bus.ValidName(s.Name) || s.Project != h.project {
		return termhost.Term{}, termhost.ErrInvalidName
	}
	r, err := h.call(ctx, h.key(), Start{Role: s.Name, Profile: s.Profile, ResumeSession: s.ResumeSession, Cols: s.Cols, Rows: s.Rows})
	if err != nil {
		return termhost.Term{}, err
	}
	if r.Term == nil {
		return termhost.Term{}, errors.New("holder: a start without its terminal")
	}
	return h.term(h.key(), *r.Term), nil
}

// List is the project's terminals on every holder that answers; a holder
// that does not answer is left out (Describe says which answered).
func (h *Host) List(ctx context.Context, project string) ([]termhost.Term, error) {
	if project != h.project {
		return nil, nil
	}
	_, keys, err := h.sockets()
	if err != nil {
		return nil, err
	}
	var out []termhost.Term
	for _, key := range keys {
		r, err := h.call(ctx, key, List{})
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		for _, t := range r.Terms {
			out = append(out, h.term(key, t))
		}
	}
	return out, nil
}

// Close closes the terminal: the holder answers once its program ended
// (SIGHUP, then SIGKILL after its grace), with the terminal as it is then.
func (h *Host) Close(ctx context.Context, id termhost.TermID) error {
	key, term, err := split(id)
	if err != nil {
		return err
	}
	r, err := h.call(ctx, key, Close{Term: term})
	if err != nil {
		return err
	}
	if r.Term != nil && r.Term.Alive {
		return fmt.Errorf("holder: terminal %s still runs after its close", id)
	}
	return nil
}

func (h *Host) Screen(ctx context.Context, id termhost.TermID, lines int) (termhost.Screen, error) {
	key, term, err := split(id)
	if err != nil {
		return termhost.Screen{}, err
	}
	r, err := h.call(ctx, key, ScreenReq{Term: term, Lines: lines})
	if err != nil {
		return termhost.Screen{}, err
	}
	if r.Screen == nil {
		return termhost.Screen{}, errors.New("holder: a screen reply without its screen")
	}
	sc := r.Screen
	return termhost.Screen{Lines: sc.Lines, Styled: strings.Split(sc.Styled, "\n"), CursorRow: sc.CursorRow,
		CursorCol: sc.CursorCol, BracketedPaste: sc.BracketedPaste, AltScreen: sc.AltScreen}, nil
}

func msTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func (h *Host) Activity(ctx context.Context, id termhost.TermID) (termhost.Activity, error) {
	key, term, err := split(id)
	if err != nil {
		return termhost.Activity{}, err
	}
	r, err := h.call(ctx, key, ActivityReq{Term: term})
	if err != nil {
		return termhost.Activity{}, err
	}
	if r.Activity == nil {
		return termhost.Activity{}, errors.New("holder: an activity reply without its activity")
	}
	a := r.Activity
	return termhost.Activity{ViewerTypedAt: msTime(a.ViewerTypedAt), Draft: a.Draft, DraftSince: msTime(a.DraftSince), OutputAt: msTime(a.OutputAt),
		FocusedViewers: a.FocusedViewers}, nil
}

func (h *Host) Processes(ctx context.Context, id termhost.TermID) ([]termhost.Proc, error) {
	key, term, err := split(id)
	if err != nil {
		return nil, err
	}
	r, err := h.call(ctx, key, Processes{Term: term})
	if err != nil {
		return nil, err
	}
	out := make([]termhost.Proc, 0, len(r.Procs))
	for _, p := range r.Procs {
		out = append(out, termhost.Proc{PID: p.PID, PPID: p.PPID, Start: p.Start, Command: p.Command, CPU: time.Duration(p.CPUms) * time.Millisecond})
	}
	return out, nil
}

// Submit sends the line's kind, nonce and count, never its text: the holder
// composes the line from its own project and the terminal's role (spec
// 4.2). A wake line built for another project or agent is refused here,
// since the holder would type its own.
func (h *Host) Submit(ctx context.Context, id termhost.TermID, line termhost.Line) error {
	if !line.Valid() {
		return termhost.ErrInvalidLine
	}
	key, term, err := split(id)
	if err != nil {
		return err
	}
	f := Submit{Kind: "clear", Term: term}
	if line.Kind() == termhost.KindCompact {
		f.Kind = "compact"
	}
	if line.Kind() == termhost.KindWake {
		r, err := h.call(ctx, key, List{})
		if err != nil {
			return err
		}
		i := slices.IndexFunc(r.Terms, func(t TermInfo) bool { return t.ID == term })
		if i < 0 {
			return termhost.ErrUnknownTerm
		}
		own, err := termhost.WakeLine(h.project, r.Terms[i].Role, line.Count(), line.Nonce())
		if err != nil || own.Text() != line.Text() {
			return fmt.Errorf("%w: a wake line for another project or agent than %s's", termhost.ErrInvalidLine, r.Terms[i].Role)
		}
		f = Submit{Kind: "wake", Term: term, Nonce: line.Nonce(), Count: int(line.Count())}
	}
	_, err = h.call(ctx, key, f)
	return err
}

// WaitScreen reports whether match shows on the screen within d.
func (h *Host) WaitScreen(ctx context.Context, id termhost.TermID, match string, d time.Duration) (bool, error) {
	deadline := time.Now().Add(d)
	for {
		sc, err := h.Screen(ctx, id, 0)
		if err != nil {
			return false, err
		}
		if slices.ContainsFunc(sc.Lines, func(l string) bool { return strings.Contains(l, match) }) {
			return true, nil
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Ping reaches the current build's holder.
func (h *Host) Ping(ctx context.Context) error {
	_, err := h.call(ctx, h.key(), Ping{})
	return err
}

// Process is the current build's holder process (its pid and start time),
// as it says itself.
func (h *Host) Process(ctx context.Context) (dispatch.ProcID, error) {
	r, err := h.call(ctx, h.key(), Describe{})
	if err != nil {
		return dispatch.ProcID{}, err
	}
	if r.Info == nil || r.Info.PID <= 0 || r.Info.Start == 0 {
		return dispatch.ProcID{}, errors.New("holder: its describe names no process (an older build)")
	}
	return dispatch.ProcID{PID: r.Info.PID, Start: r.Info.Start}, nil
}

// HolderState is one holder of the project that answered Describe: its
// build and socket, and its live terminals when its List answered too
// (Listed). A List that failed leaves Listed false: never "no terminal".
type HolderState struct {
	Build  string
	Socket string
	PID    int // the holder process, as it says itself (0: an older build)
	Live   int
	Listed bool
}

// Holders is every holder of the project that answers, each with its own
// List: unlike List, a holder whose List fails is said, not skipped
// (reviewer-codex, holder-behind).
func (h *Host) Holders(ctx context.Context) ([]HolderState, error) {
	socks, keys, err := h.sockets()
	if err != nil {
		return nil, err
	}
	var out []HolderState
	for _, key := range keys {
		r, err := h.call(ctx, key, Describe{})
		if err != nil || r.Info == nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		st := HolderState{Build: r.Info.Build, Socket: socks[key], PID: r.Info.PID}
		if l, err := h.call(ctx, key, List{}); err == nil {
			st.Listed = true
			for _, t := range l.Terms {
				if t.Alive {
					st.Live++
				}
			}
		} else if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		out = append(out, st)
	}
	return out, nil
}

// Describe is every holder of the project that answers: its build, socket
// and terminals; a build other than the current one is draining.
func (h *Host) Describe(ctx context.Context) (termhost.HostInfo, error) {
	socks, keys, err := h.sockets()
	if err != nil {
		return termhost.HostInfo{}, err
	}
	var info termhost.HostInfo
	for _, key := range keys {
		r, err := h.call(ctx, key, Describe{})
		if err != nil || r.Info == nil {
			if ctx.Err() != nil {
				return termhost.HostInfo{}, ctx.Err()
			}
			continue
		}
		info.Builds = append(info.Builds, r.Info.Build)
		info.Sockets = append(info.Sockets, socks[key])
		info.Terms += r.Info.Terms
		if key != h.key() {
			info.Draining = append(info.Draining, r.Info.Build)
		}
	}
	return info, nil
}
