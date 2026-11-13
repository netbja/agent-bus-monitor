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

// ActivityInfo is a terminal's last output (ms since the epoch, 0 none).
type ActivityInfo struct {
	OutputAt int64 `json:"output_at"`
}

// ProcInfo is one process of a terminal's tree (spec 4.3b).
type ProcInfo struct {
	PID     int    `json:"pid"`
	PPID    int    `json:"ppid"`
	Start   uint64 `json:"start"`
	Command string `json:"command"`
	CPUms   int64  `json:"cpu_ms"`
}

// Info describes the holder (Describe).
type Info struct {
	Project string `json:"project"`
	Build   string `json:"build"`
	Proto   int    `json:"proto"`
	Terms   int    `json:"terms"`
	Socket  string `json:"socket"`
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
// ("+dirty" for a modified tree).
func BuildID() string {
	id := buildinfo.Version
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return id
	}
	rev, dirty := "", false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
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
