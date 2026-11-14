package holder

// holdMax bounds the held viewer bytes of a terminal (spec 4.2, correction
// r25): what is held is never dropped; past 1 MiB every further byte is
// refused until the hold ends, counted and told, and the holder keeps reading every viewer,
// so its Release, Discard and Detach always get through.
const holdMax = 1 << 20

// The hold (spec 4.2, security S5). During a submit's critical section the
// input lock is the hold: every viewer byte waits on it, in arrival order,
// and is delivered after the CR. After a line that did not land
// (ErrNotLanded) the hold outlives the lock: viewer bytes queue here, and
// each typing viewer is told what may be left in the input, until <prefix>
// r releases them in order, <prefix> x drops them, or a settled screen shows
// the input empty (step 5d).
type holdState struct {
	holding  bool
	holdKind string // "wake" or "clear"
	held     []byte
	holdMax  int // the bound (holdMax; a test sets it small)
	refused  int // bytes refused since the hold began: never delivered
}

// deliver writes a viewer's bytes to the program, or queues them while a
// hold stands. The input lock is taken first and the hold read under it, so
// a byte never overtakes one held before it. Bytes that would take the
// queue past its bound are refused and counted, and so is every later byte
// until the hold ends (never held behind the hole), and the viewers told: deliver
// never waits, so the viewer's next frame (its Release, Discard, Detach) is
// always read.
func (t *terminal) deliver(b []byte) {
	t.inMu.Lock()
	defer t.inMu.Unlock()
	t.mu.Lock()
	if !t.holding {
		t.mu.Unlock()
		_, _ = t.pty.Write(b)
		return
	}
	defer t.mu.Unlock()
	if t.refused == 0 && len(t.held)+len(b) <= t.holdMax { // once refused, refused until the hold ends: nothing queued behind a hole
		t.held = append(t.held, b...)
	} else {
		t.refused += len(b)
	}
	t.noticeAll()
}

// startHold begins a hold for a line of kind that did not land; the caller
// holds the input lock (Submit, step 5d).
func (t *terminal) startHold(kind string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.holding, t.holdKind = true, kind
	t.noticeAll()
}

// endHold ends the hold: its bytes delivered in order (release) or dropped
// (discard). Nothing held: nothing happens.
func (t *terminal) endHold(release bool) {
	t.inMu.Lock()
	defer t.inMu.Unlock()
	t.mu.Lock()
	if !t.holding {
		t.mu.Unlock()
		return
	}
	b := t.held
	t.holding, t.held, t.refused = false, nil, 0
	t.noticeAll()
	t.mu.Unlock()
	if release && len(b) > 0 {
		_, _ = t.pty.Write(b)
	}
}

// noticeAll tells every typing viewer the hold's state. Called with t.mu held.
func (t *terminal) noticeAll() {
	for v := range t.viewers {
		if !v.observe {
			t.noticeTo(v)
		}
	}
}

// noticeTo queues the hold's state for v; a full control queue keeps the
// older notice (the next one carries the state again). Called with t.mu held.
func (t *terminal) noticeTo(v *viewer) {
	kind := t.holdKind
	if kind == "" {
		kind = "clear"
	}
	b, err := EncodeControl(HoldNotice{Kind: kind, Held: len(t.held), Active: t.holding, Refused: t.refused})
	if err != nil {
		return
	}
	select {
	case v.ctl <- b:
	default:
	}
}
