// Package termhost is the seam between agentbus and whatever hosts the
// agents' terminals (spec docs/superpowers/specs/2026-11-10-herdr-exit-design.md,
// 4.2): one interface, the holder's adapter (a later step) and an in-memory
// fake for tests. Nothing in the running system uses a Host yet; only Line
// already crosses dispatch's Runtime.
//
// Every call reports what it observed, never what it assumed.
package termhost

import (
	"context"
	"errors"
	"time"
)

// TermID identifies one terminal, unique for its host's lifetime.
type TermID string

// Spec is a terminal to start: role Name's launch in project (both
// bus.ValidName), optionally under another of its profiles and resuming a
// recorded session. It carries no argv, env or cwd (security S2a): the host
// builds the command from the role's launch template.
type Spec struct {
	Project, Name string
	Profile       string // "": the role's own
	ResumeSession string // "": a fresh session
	Cols, Rows    int
}

// Term is a terminal as the host sees it.
type Term struct {
	ID            TermID
	Project, Name string
	PID           int    // the program the host spawned
	PIDStart      uint64 // its start time, for the verified pid+start rule
	Alive         bool
	Viewers       int
	Build         string // the holder build that owns it
}

// Screen is a snapshot of a terminal's rendered lines.
type Screen struct {
	Lines          []string // text
	Styled         []string // the same lines, SGR-styled
	CursorRow      int
	CursorCol      int
	BracketedPaste bool // the program enabled mode 2004
	AltScreen      bool
	FocusReporting bool
}

// Activity is a terminal's recent input and output (spec 4.5).
type Activity struct {
	ViewerTypedAt time.Time // a viewer's last byte; zero: never
	Draft         bool      // a viewer typed since the last CR
	DraftSince    time.Time
	OutputAt      time.Time // the program's last output
}

// PersonActive reports whether a person is at the terminal: a draft stands,
// or a viewer typed within quiet of now.
func (a Activity) PersonActive(now time.Time, quiet time.Duration) bool {
	return a.Draft || (!a.ViewerTypedAt.IsZero() && now.Sub(a.ViewerTypedAt) < quiet)
}

// Proc is one process of a terminal's tree (spec 4.3b).
type Proc struct {
	PID     int
	PPID    int
	Start   uint64
	Command string
	CPU     time.Duration
}

// HostInfo describes the host for reports (health, About).
type HostInfo struct {
	Builds   []string
	Sockets  []string
	Terms    int
	Draining []string
}

// The errors a Host returns; Submit's are part of its contract (spec 4.2).
var (
	ErrUnknownTerm  = errors.New("termhost: no such terminal")
	ErrInvalidName  = errors.New("termhost: invalid project or terminal name")
	ErrInvalidLine  = errors.New("termhost: not a wake line or /clear")
	ErrPersonActive = errors.New("termhost: a person is active at the terminal")
	ErrNotReady     = errors.New("termhost: the program is not ready or its input is not empty")
	ErrNotLanded    = errors.New("termhost: the line was not seen in the input; nothing more sent")
	ErrLeftBehind   = errors.New("termhost: a line may be left in the input; no submit until it reads empty")
)

// Host owns terminals. Its rules (spec 4.2) are part of the interface:
//
//   - Start runs spec as a new terminal; it never reuses one, and refuses a
//     project or name outside bus.ValidName.
//   - Submit types a Line, never text. Under the terminal's input lock it
//     checks that no person is active (ErrPersonActive), that the program is
//     ready and its input empty (ErrNotReady), and that the terminal is not
//     left behind (ErrLeftBehind); it holds every viewer byte, pastes the
//     line, waits until the line is seen in the input, and only then sends
//     CR. A line not seen sends nothing more, marks the terminal left behind
//     and returns ErrNotLanded; the mark clears only once a settled screen
//     shows the input empty.
//   - Close sends SIGHUP, then SIGKILL after a grace period.
type Host interface {
	Start(ctx context.Context, spec Spec) (Term, error)
	List(ctx context.Context, project string) ([]Term, error)
	Close(ctx context.Context, id TermID) error
	Screen(ctx context.Context, id TermID, lines int) (Screen, error)
	Activity(ctx context.Context, id TermID) (Activity, error)
	Processes(ctx context.Context, id TermID) ([]Proc, error)
	Submit(ctx context.Context, id TermID, line Line) error
	WaitScreen(ctx context.Context, id TermID, match string, d time.Duration) (bool, error)
	Ping(ctx context.Context) error
	Describe(ctx context.Context) (HostInfo, error)
}
