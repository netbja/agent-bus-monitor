package termhost

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/netbja/agent-bus-monitor/bus"
)

// LineKind is what a Line types: the wake line or /clear (spec
// 2026-11-10-herdr-exit-design.md, 4.2). The holder's submit frame carries
// the kind, never text.
type LineKind int

const (
	KindNone  LineKind = iota // the zero Line: never submitted
	KindWake                  // the wake line
	KindClear                 // /clear, after a declared hand-off
)

// MaxCount is the highest count a wake line shows; a larger backlog reads
// "999+" (capped, never refused: the agent that needs it most still wakes).
const MaxCount = 999

// clearText is the only thing a clear line types.
const clearText = "/clear"

// wakeFormat is the wake line, the one the dispatcher has always typed:
// nonce, project, count, agent, project, agent.
const wakeFormat = "[agentbus#%s] %s: %s message(s) waiting for %s. Run: agentbus --project %s subscribe --drain %s"

var nonceRE = regexp.MustCompile(`^[0-9a-f]{6,16}$`)

// Line is one of the two lines a host may type into a terminal. Its fields
// are unexported and its only constructors are WakeLine and ClearLine, so no
// caller can make it carry other text (TestLineIsAClosedSet).
type Line struct {
	kind           LineKind
	project, agent string
	nonce          string
	count          int64
}

// WakeLine is the wake line for agent of project: count messages waiting,
// marked with nonce. It refuses a project or agent outside bus.ValidName, a
// count below 1 and a nonce outside ^[0-9a-f]{6,16}$, so nothing outside
// those alphabets reaches the typed line; a count above MaxCount is capped.
func WakeLine(project, agent string, count int64, nonce string) (Line, error) {
	switch {
	case !bus.ValidName(project):
		return Line{}, errors.New("wake line: invalid project name")
	case !bus.ValidName(agent):
		return Line{}, errors.New("wake line: invalid agent name")
	case count < 1:
		return Line{}, fmt.Errorf("wake line: count %d below 1", count)
	case !nonceRE.MatchString(nonce):
		return Line{}, errors.New("wake line: the nonce must match ^[0-9a-f]{6,16}$")
	}
	return Line{kind: KindWake, project: project, agent: agent, nonce: nonce, count: min(count, MaxCount+1)}, nil
}

// ClearLine is /clear.
func ClearLine() Line { return Line{kind: KindClear} }

// Kind is the line's kind; KindNone for the zero Line.
func (l Line) Kind() LineKind { return l.kind }

// Valid reports whether l was made by WakeLine or ClearLine.
func (l Line) Valid() bool { return l.kind == KindWake || l.kind == KindClear }

// Nonce is a wake line's nonce ("" for /clear).
func (l Line) Nonce() string { return l.nonce }

// Count is the count a wake line shows, at most MaxCount.
func (l Line) Count() int64 { return min(l.count, MaxCount) }

// Text is the line as typed; "" for the zero Line.
func (l Line) Text() string {
	switch l.kind {
	case KindWake:
		n := fmt.Sprint(l.count)
		if l.count > MaxCount {
			n = fmt.Sprintf("%d+", MaxCount)
		}
		return fmt.Sprintf(wakeFormat, l.nonce, l.project, n, l.agent, l.project, l.agent)
	case KindClear:
		return clearText
	}
	return ""
}

// Marker is what shows the line landed: a wake line's "[agentbus#<nonce>]",
// or /clear itself.
func (l Line) Marker() string {
	switch l.kind {
	case KindWake:
		return "[agentbus#" + l.nonce + "]"
	case KindClear:
		return clearText
	}
	return ""
}
