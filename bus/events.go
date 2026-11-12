package bus

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/redis/go-redis/v9"
)

// {p}:events (herdr exit step 8, spec 2026-11-10-herdr-exit-design.md 6.5):
// the terminal-lifecycle facts the terminal host adds — a wake typed or
// refused, blocked and unblocked, a draft waiting, an action skipped for
// background work, attach and detach, relaunch, the holder's life. One stream
// per project, its time the entry id (Redis TIME). A timeline, never a source
// of truth: state stays in {p}:agents, {p}:observed and {p}:resume. Not an
// activity stream (a wake attempt must not date a project as active), and not
// part of busmon's ACTIVITY feed.

// EventsKey is the project's event stream.
func EventsKey(project string) string { return project + ":events" }

// The event kinds (spec 6.5); AppendEvent refuses any other.
const (
	EventWakeTyped       = "wake"
	EventWakeRefused     = "wake-refused"
	EventWakeNotLanded   = "wake-not-landed"
	EventBlocked         = "blocked"
	EventUnblocked       = "unblocked"
	EventDraftWaiting    = "draft-waiting"
	EventBackgroundSkip  = "background-skip"
	EventAttach          = "attach"
	EventDetach          = "detach"
	EventRelaunch        = "relaunch"
	EventHolderStarted   = "holder-started"
	EventHolderRestarted = "holder-restarted"
	EventHolderDraining  = "holder-draining"
	EventHolderDrained   = "holder-drained"
	EventHolderGC        = "holder-gc"
)

var eventKinds = map[string]bool{
	EventWakeTyped: true, EventWakeRefused: true, EventWakeNotLanded: true, EventBlocked: true, EventUnblocked: true,
	EventDraftWaiting: true, EventBackgroundSkip: true, EventAttach: true, EventDetach: true, EventRelaunch: true,
	EventHolderStarted: true, EventHolderRestarted: true, EventHolderDraining: true, EventHolderDrained: true, EventHolderGC: true,
}

// eventsMaxLen caps the stream (XADD MAXLEN ~).
const eventsMaxLen = 10000

// eventDetailMax caps a detail, which may quote an agent's screen.
const eventDetailMax = 300

// termNamePattern is a terminal's name, as the holder will give it.
var termNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

// TermEvent is one entry. Agent, Term and By may be empty (a holder's gc
// names no agent); At and ID are the entry's, set on reading.
type TermEvent struct {
	ID     string
	At     time.Time
	Kind   string
	Agent  string
	Term   string
	By     string
	Detail string
}

// eventDetail makes a detail one line of data: control and format
// characters dropped, line breaks as spaces, capped.
func eventDetail(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteRune(' ')
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if rs := []rune(out); len(rs) > eventDetailMax {
		out = string(rs[:eventDetailMax-1]) + "…"
	}
	return out
}

// AppendEvent writes one event, unfenced: for a person's own process
// (attach, detach) and the commands a person runs (up, holder drain, service
// gc). The dispatcher's events will go through its fenced writes (step 9).
func (b *Bus) AppendEvent(ctx context.Context, e TermEvent) (string, error) {
	if !eventKinds[e.Kind] {
		return "", fmt.Errorf("event: unknown kind %q", e.Kind)
	}
	if e.Agent != "" && !ValidName(e.Agent) {
		return "", fmt.Errorf("event: invalid agent %q", e.Agent)
	}
	if e.Term != "" && !termNamePattern.MatchString(e.Term) {
		return "", fmt.Errorf("event: invalid terminal %q", e.Term)
	}
	if e.By != "" && !ValidName(e.By) {
		return "", fmt.Errorf("event: invalid author %q", e.By)
	}
	return b.r.XAdd(ctx, &redis.XAddArgs{Stream: EventsKey(b.project), MaxLen: eventsMaxLen, Approx: true,
		Values: map[string]any{"kind": e.Kind, "agent": e.Agent, "term": e.Term, "by": e.By, "detail": eventDetail(e.Detail)}}).Result()
}

// RecentEvents are the last n events, newest first.
func (b *Bus) RecentEvents(ctx context.Context, n int64) ([]TermEvent, error) {
	if n <= 0 {
		return nil, errors.New("events: n must be positive")
	}
	msgs, err := b.r.XRevRangeN(ctx, EventsKey(b.project), "+", "-", n).Result()
	if err != nil {
		return nil, err
	}
	out := make([]TermEvent, 0, len(msgs))
	for _, m := range msgs {
		v := toStringMap(m.Values)
		ms, _ := splitID(m.ID)
		out = append(out, TermEvent{ID: m.ID, At: time.UnixMilli(ms), Kind: v["kind"], Agent: v["agent"], Term: v["term"], By: v["by"], Detail: v["detail"]})
	}
	return out, nil
}

// ResetKinds are the streams busmon --reset trims (XTRIM, groups kept):
// the activity streams and the event timeline. SurvivesReset is every other
// project kind, named on purpose: a reset trims history, it never forgets
// state (resume above all: how to resume each agent).
// TestEveryProjectKindIsResetOrSurvives fails on a project kind in neither.
var (
	ResetKinds    = []string{"status", "report", "notify", "cmd", "events"}
	SurvivesReset = []string{"agents", "usage", "budget", "verdicts", "pilot", "board", "dispatch", "conductor",
		"missionops", "services", "pause", "quota", "asks", "resume"}
)

// ErrConfigRefused: the broker answered CONFIG GET with an error reply of its
// own (an unknown or renamed command, NOPERM on a managed broker), or with no
// value. A connection or context failure is never this.
var ErrConfigRefused = errors.New("the broker refused CONFIG GET")

// configErr tells a refusal (an error reply from the server) from a failure
// to reach it, which is returned as it is (reviewer-codex).
func configErr(err error) error {
	var reply redis.Error
	if errors.As(err, &reply) {
		return fmt.Errorf("%w: %v", ErrConfigRefused, err)
	}
	return err
}

// Persistence is the broker's persistence settings: CONFIG GET appendonly
// and save; ErrConfigRefused when the broker refuses them.
func (b *Bus) Persistence(ctx context.Context) (appendOnly, save string, err error) {
	aof, err := b.r.ConfigGet(ctx, "appendonly").Result()
	if err != nil {
		return "", "", configErr(err)
	}
	sv, err := b.r.ConfigGet(ctx, "save").Result()
	if err != nil {
		return "", "", configErr(err)
	}
	a, ok1 := aof["appendonly"]
	s, ok2 := sv["save"]
	if !ok1 || !ok2 {
		return "", "", fmt.Errorf("%w: no value", ErrConfigRefused)
	}
	return a, s, nil
}
