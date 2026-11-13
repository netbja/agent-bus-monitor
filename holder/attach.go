package holder

import (
	"net"
	"time"

	"github.com/creack/pty"
)

// viewerQueue bounds a viewer's pending output chunks; a viewer that falls
// further behind is repainted from the emulator instead (spec 4.4, a slow
// viewer never blocks a program).
const viewerQueue = 256

// viewer is one attach (spec 4.6). An observer never types, never resizes
// and never counts as a person at the terminal.
type viewer struct {
	observe    bool
	focused    bool // focused from the moment it attaches, until a focus-out (spec 4.5)
	cols, rows int
	out        chan []byte // filtered output, in order
	repaint    bool        // fell behind: its next write is a first paint
	filter     outputFilter
}

// addViewer registers v and queues its first paint, rendered from the
// emulator: what an empty outer terminal needs to show the screen.
func (t *terminal) addViewer(v *viewer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.viewers == nil {
		t.viewers = map[*viewer]bool{}
	}
	t.viewers[v] = true
	v.out <- t.scr.Render()
	if !v.observe && v.cols > 0 {
		t.activeViewer(v)
	}
}

func (t *terminal) removeViewer(v *viewer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.viewers, v)
	if t.last == v {
		t.last = nil
	}
}

// fanOut hands one output chunk to every viewer, filtered for it; called
// with t.mu held, after the emulator took the chunk. A full queue marks the
// viewer for a repaint instead of blocking the program.
func (t *terminal) fanOut(chunk []byte) {
	for v := range t.viewers {
		if v.repaint {
			continue
		}
		b := v.filter.filter(chunk)
		if len(b) == 0 {
			continue
		}
		select {
		case v.out <- b:
		default:
			v.repaint = true
		}
	}
}

// nextFor is what to write to v next: a repaint (its stale chunks dropped,
// the screen rendered now, with t.mu held so nothing is lost or doubled)
// when it fell behind, else its next chunk. ok is false once the terminal
// ended and v's queue is empty, or the viewer left (stop).
func (t *terminal) nextFor(v *viewer, done, stop <-chan struct{}) ([]byte, bool) {
	t.mu.Lock()
	if v.repaint {
		for len(v.out) > 0 {
			<-v.out
		}
		v.repaint, v.filter = false, outputFilter{osc52: v.filter.osc52}
		b := t.scr.Render()
		t.mu.Unlock()
		return b, true
	}
	t.mu.Unlock()
	select {
	case b := <-v.out:
		return b, true
	case <-stop:
		return nil, false
	case <-done:
		select {
		case b := <-v.out:
			return b, true
		default:
			return nil, false
		}
	}
}

// activeViewer makes v the most recently active viewer: its size is the
// terminal's (spec 4.6). Called with t.mu held.
func (t *terminal) activeViewer(v *viewer) {
	t.last = v
	if v.cols > 0 && (v.cols != t.cols || v.rows != t.rows) {
		t.resize(v.cols, v.rows)
	}
}

// resize sets the PTY's and the emulator's size. Called with t.mu held.
func (t *terminal) resize(cols, rows int) {
	t.cols, t.rows = cols, rows
	_ = pty.Setsize(t.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	t.scr.Resize(cols, rows)
}

// input writes a viewer's bytes to the program under the input lock (the
// lock Submit takes too, step 5d) and records the activity: the time it
// typed and the draft (spec 4.5). Mouse and focus reports are no typing.
func (t *terminal) input(v *viewer, b []byte) {
	t.inMu.Lock()
	_, _ = t.pty.Write(b)
	t.inMu.Unlock()
	t.mu.Lock()
	defer t.mu.Unlock()
	draft, typed := draftAfter(t.draft, b)
	if typed {
		t.typedAt = time.Now()
		t.activeViewer(v)
	}
	if draft && !t.draft {
		t.draftSince = time.Now()
	}
	t.draft = draft
}

// draftAfter is the draft once b is typed (spec 4.5): any byte starts one,
// an Enter (CR, or the kitty encoding CSI 13 u) ends it; SGR and X10 mouse
// reports and focus reports are skipped. typed: b held a byte that is not
// such a report.
func draftAfter(draft bool, b []byte) (bool, bool) {
	typed := false
	for i := 0; i < len(b); i++ {
		if b[i] == 0x1b && i+2 < len(b) && b[i+1] == '[' {
			j := i + 2
			switch {
			case b[j] == '<': // SGR mouse: CSI < … M or m
				k := j + 1
				for k < len(b) && b[k] != 'M' && b[k] != 'm' {
					k++
				}
				if k < len(b) {
					i = k
					continue
				}
			case b[j] == 'M' && j+3 < len(b): // X10 mouse: CSI M and three bytes
				i = j + 3
				continue
			case b[j] == 'I' || b[j] == 'O': // focus in, focus out
				i = j
				continue
			}
			k := j
			for k < len(b) && (b[k] >= '0' && b[k] <= '9' || b[k] == ';' || b[k] == ':') {
				k++
			}
			if k < len(b) && b[k] == 'u' && (string(b[j:k]) == "13" || len(b[j:k]) > 3 && string(b[j:j+3]) == "13;") {
				draft, typed, i = false, true, k // kitty Enter
				continue
			}
		}
		typed = true
		if b[i] == '\r' {
			draft = false
		} else {
			draft = true
		}
	}
	return draft, typed
}

// activity is the terminal's ActivityInfo: its output, the viewers' typing
// and draft, and the viewers that may be at it (non-observers not proven
// unfocused, spec 4.5).
func (t *terminal) activity() *ActivityInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	a := &ActivityInfo{Draft: t.draft, Viewers: len(t.viewers)}
	ms := func(at time.Time) int64 {
		if at.IsZero() {
			return 0
		}
		return at.UnixMilli()
	}
	a.OutputAt, a.ViewerTypedAt = ms(t.outAt), ms(t.typedAt)
	if t.draft {
		a.DraftSince = ms(t.draftSince)
	}
	for v := range t.viewers {
		if !v.observe && v.focused {
			a.FocusedViewers++
		}
	}
	return a
}

// attach serves one viewer on c until it detaches, its connection ends or
// the terminal ends: the reply, a first paint, then the filtered output as
// data frames; from the viewer, its input (never an observer's) and its
// focus, resize and detach frames.
func (s *Server) attach(c *net.UnixConn, t *terminal, f Attach) {
	v := &viewer{observe: f.Observe, focused: !f.Observe, cols: f.Cols, rows: f.Rows,
		out: make(chan []byte, viewerQueue), filter: outputFilter{osc52: s.cfg.OSC52}}
	if err := WriteFrame(c, FrameControl, Reply{}.encode()); err != nil {
		return
	}
	t.addViewer(v)
	defer t.removeViewer(v)
	stop := make(chan struct{})
	go func() {
		defer close(stop)
		for {
			typ, body, err := ReadFrame(c)
			if err != nil {
				return
			}
			if typ == FrameData {
				if !v.observe {
					t.input(v, body)
				}
				continue
			}
			req, err := DecodeControl(body)
			if err != nil {
				continue
			}
			switch r := req.(type) {
			case Detach:
				return
			case Focus:
				t.mu.Lock()
				v.focused = r.In && !v.observe
				t.mu.Unlock()
			case Resize:
				if v.observe {
					continue // an observer never resizes
				}
				t.mu.Lock()
				v.cols, v.rows = r.Cols, r.Rows
				if t.last == v {
					t.resize(r.Cols, r.Rows)
				}
				t.mu.Unlock()
			}
		}
	}()
	for {
		b, ok := t.nextFor(v, t.outDone, stop)
		if !ok {
			return
		}
		if err := WriteFrame(c, FrameData, b); err != nil {
			return
		}
	}
}
