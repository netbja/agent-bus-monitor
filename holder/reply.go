package holder

import (
	"encoding/json"
	"fmt"
	"runtime/debug"

	"github.com/netbja/agent-bus-monitor/buildinfo"
)

// Reply is the holder's answer to a control frame ({"op":"reply"}): an
// error and its code, or what the op asked for. The holder writes it; a
// client reads it from a holder it has checked (same uid).
type Reply struct {
	Op       string        `json:"op"`
	Error    string        `json:"error,omitempty"`
	Code     string        `json:"code,omitempty"`
	Term     *TermInfo     `json:"term,omitempty"`
	Terms    []TermInfo    `json:"terms,omitempty"`
	Screen   *ScreenInfo   `json:"screen,omitempty"`
	Activity *ActivityInfo `json:"activity,omitempty"`
	Procs    []ProcInfo    `json:"procs,omitempty"`
	Info     *Info         `json:"info,omitempty"`
}

// The error codes a reply carries.
const (
	CodeInTree   = "in_tree"   // a process inside a terminal asked for a write (security S2b)
	CodeUnknown  = "unknown"   // no such terminal
	CodeRefused  = "refused"   // the frame's values were refused (a role, a profile, a session)
	CodeBadFrame = "bad_frame" // the frame itself
	CodeLater    = "later"     // not served yet (submit and attach: herdr exit step 5c)
	CodeFailed   = "failed"    // the operation failed
	CodeDraining = "draining"  // an older build: no new terminal
	CodeScreen   = "screen"    // the screen is not vouched for (lagging, invalid, stuck): termhost.ErrScreenUntrusted
)

// TermInfo is one terminal.
type TermInfo struct {
	ID       string `json:"id"`
	Role     string `json:"role"`
	PID      int    `json:"pid"`
	PIDStart uint64 `json:"pid_start"`
	Alive    bool   `json:"alive"`
	Exit     *int   `json:"exit,omitempty"` // the program's exit status once it ended (-1: by a signal)
	Viewers  int    `json:"viewers"`
	Build    string `json:"build"`
	// Profile is the client's agentstate profile (claude, codex, …; "" none):
	// agentbus attach gates its prefix on it (spec 4.6).
	Profile string `json:"profile,omitempty"`
	// Screen is whether the holder vouches for its screen (holder hardening
	// G1): ok, lagging (the emulator behind, for now), invalid or stuck
	// (latched until the terminal is replaced). Anything but ok reads
	// unknown: never woken, never submitted to. ScreenGen is raised when
	// the state is latched.
	Screen    string `json:"screen,omitempty"`
	ScreenGen int    `json:"screen_gen,omitempty"`
}

// ScreenInfo is a terminal's screen.
type ScreenInfo struct {
	Lines          []string `json:"lines"`
	Styled         string   `json:"styled"`
	CursorRow      int      `json:"cursor_row"`
	CursorCol      int      `json:"cursor_col"`
	BracketedPaste bool     `json:"bracketed_paste"`
	AltScreen      bool     `json:"alt_screen"`
}

// ActivityInfo is a terminal's input and output (spec 4.5; times in ms
// since the epoch, 0 none): its last output, a viewer's last typing, the
// draft and since when, the viewers, and how many of them may be at it
// (non-observers not proven unfocused).
type ActivityInfo struct {
	OutputAt       int64  `json:"output_at"`
	ViewerTypedAt  int64  `json:"viewer_typed_at,omitempty"`
	Draft          bool   `json:"draft,omitempty"`
	DraftSince     int64  `json:"draft_since,omitempty"`
	Viewers        int    `json:"viewers,omitempty"`
	FocusedViewers int    `json:"focused_viewers,omitempty"`
	Screen         string `json:"screen,omitempty"` // as TermInfo.Screen (holder hardening G1)
}

// ProcInfo is one process of a terminal's tree (spec 4.3b).
type ProcInfo struct {
	PID     int    `json:"pid"`
	PPID    int    `json:"ppid"`
	Start   uint64 `json:"start"`
	Command string `json:"command"`
	CPUms   int64  `json:"cpu_ms"`
	RSSkb   int64  `json:"rss_kb,omitempty"` // resident memory (agent-resource-limits §5)
}

// Info describes the holder (Describe).
type Info struct {
	Project string `json:"project"`
	Build   string `json:"build"`
	Proto   int    `json:"proto"`
	Terms   int    `json:"terms"`
	Socket  string `json:"socket"`
	// The holder's own process, so a live check can crash it as a crash
	// does (SIGKILL; holder hardening §5); the start time guards a reused pid.
	PID   int    `json:"pid,omitempty"`
	Start uint64 `json:"start,omitempty"`
	// Memory is its unit's (agent-resource-limits §3.1); none without one.
	Memory *UnitMemory `json:"memory,omitempty"`
}

func errReply(code string, format string, a ...any) Reply {
	return Reply{Op: "reply", Code: code, Error: fmt.Sprintf(format, a...)}
}

func (r Reply) encode() []byte {
	r.Op = "reply"
	b, _ := json.Marshal(r)
	return b
}

// BuildID is this binary's build id, as hello and welcome carry it: the
// buildinfo version, then the VCS revision when the build recorded one
// ("+dirty" for a modified tree: setup's stamped buildinfo.Tree, which
// ignores untracked files, else Go's vcs.modified).
func BuildID() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return buildinfo.Version
	}
	return buildID(buildinfo.Version, buildinfo.Tree, bi.Settings)
}

func buildID(version, tree string, settings []debug.BuildSetting) string {
	id := version
	rev, dirty := "", false
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if tree != "" { // setup's stamp, git's tracked view: untracked files never count
		dirty = tree == "dirty"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if rev != "" {
		id += "+" + rev
	}
	if dirty {
		id += "+dirty"
	}
	return id
}
