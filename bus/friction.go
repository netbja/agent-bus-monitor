package bus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Friction kinds (self-improving loop spec §3.1): the facts the bus already
// holds that say a task went badly. Counted, never guessed.
const (
	FrictionReject = "reject" // a reject verdict on the owner's work
	FrictionCI     = "ci"     // a CI hand-off to the owner
	FrictionStall  = "stall"  // a blocked or wake-not-landed event, or an escalation naming the owner
	FrictionPerson = "person" // the person asked for a retro (agentbus retro request)
)

var frictionOrder = []string{FrictionReject, FrictionCI, FrictionStall, FrictionPerson}

// The streams a signal is read from: ids are unique only within a stream.
const (
	SignalVerdicts = "verdicts"
	SignalCmd      = "cmd"
	SignalEvents   = "events"
)

// FrictionSignal is one signal: its kind, the id that holds it and that id's
// stream ("" for the person's signal).
type FrictionSignal struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Stream string `json:"stream,omitempty"`
}

func (s FrictionSignal) String() string {
	if s.Stream == "" {
		return s.Kind + " " + s.ID
	}
	return s.Kind + " " + s.ID + " (" + s.Stream + ")"
}

// TaskFriction is task's friction: the signals of its owner (the request's
// target) inside the request's life, from its accept to its done (to now
// while it is open), oldest first. A request never accepted has none. Only
// what the capped streams still hold is read.
func (b *Bus) TaskFriction(ctx context.Context, task string) ([]FrictionSignal, error) {
	raw, err := b.r.HGet(ctx, BoardKey(b.project), task).Result()
	if errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("friction: unknown task %q", task)
	}
	if err != nil {
		return nil, err
	}
	var e BoardEntry
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		return nil, fmt.Errorf("friction: task %q: %w", task, err)
	}
	r := e.Request
	if r == nil || r.AcceptedAt == 0 {
		return nil, nil
	}
	end := r.RespondedAt
	if end == 0 {
		now, err := b.r.Time(ctx).Result()
		if err != nil {
			return nil, err
		}
		end = now.UnixMilli()
	}
	owner := r.Target
	from, to := strconv.FormatInt(r.AcceptedAt, 10), strconv.FormatInt(end, 10)

	var out []FrictionSignal
	verdicts, err := b.r.XRange(ctx, VerdictsKey(b.project), from, to).Result()
	if err != nil {
		return nil, err
	}
	for _, m := range verdicts {
		v := toStringMap(m.Values)
		if v["decision"] == "reject" && v["author"] == owner {
			out = append(out, FrictionSignal{FrictionReject, m.ID, SignalVerdicts})
		}
	}
	cmds, err := b.r.XRange(ctx, StreamKey(b.project, "cmd"), from, to).Result()
	if err != nil {
		return nil, err
	}
	for _, m := range cmds {
		v := toStringMap(m.Values)
		switch {
		case isCIHandoff(v) && v["target"] == owner:
			out = append(out, FrictionSignal{FrictionCI, m.ID, SignalCmd})
		case v["type"] == CmdEscalation && v["agent"] == owner:
			out = append(out, FrictionSignal{FrictionStall, m.ID, SignalCmd})
		}
	}
	events, err := b.r.XRange(ctx, EventsKey(b.project), from, to).Result()
	if err != nil {
		return nil, err
	}
	for _, m := range events {
		v := toStringMap(m.Values)
		if (v["kind"] == EventBlocked || v["kind"] == EventWakeNotLanded) && v["agent"] == owner {
			out = append(out, FrictionSignal{FrictionStall, m.ID, SignalEvents})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return idLess(out[i].ID, out[j].ID) })
	return out, nil
}

// isCIHandoff: the CI loop's directive to a branch's owner (FencedCIStep):
// from dispatch, episode ci:…, and not master's wake=no copy.
func isCIHandoff(v map[string]string) bool {
	return v["from"] == dispatchFrom && v["type"] == CmdDirective && strings.HasPrefix(v["episode"], "ci:") && v["wake"] != "no"
}

const dispatchFrom = "dispatch"

// FrictionSummary counts signals per kind, in a fixed order: "2 reject, 1 ci".
func FrictionSummary(sigs []FrictionSignal) string {
	n := map[string]int{}
	for _, s := range sigs {
		n[s.Kind]++
	}
	var parts []string
	for _, k := range frictionOrder {
		if n[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n[k], k))
		}
	}
	return strings.Join(parts, ", ")
}

// RetroPrefix starts every retro's task name.
const RetroPrefix = "retro-"

// IsRetroTask: task is a retro, whose own done never fires a retro (§3.2).
func IsRetroTask(task string) bool { return strings.HasPrefix(task, RetroPrefix) }

// RetroTaskName is retro-<task>. For a task too long for a valid name (32
// characters) it is retro-<the task's first 17 characters>-<8 hex of the
// task's sha256>, so two long tasks never share a retro.
func RetroTaskName(task string) string {
	name := RetroPrefix + task
	if len(name) <= 32 {
		return name
	}
	sum := sha256.Sum256([]byte(task))
	return RetroPrefix + strings.TrimRight(task[:17], "-_") + "-" + hex.EncodeToString(sum[:4])
}

// Retro is what RetroDue found: the retro's task ("" when none is due), the
// signals, its owner (To), and whether this call sent its request (false
// when it existed).
type Retro struct {
	Task    string
	Signals []FrictionSignal
	To      string
	Sent    bool
}

// RetroDue runs after a task's done (§3.2): with no friction, or for a retro
// itself, nothing; otherwise one tracked request retro-<task>, from agentbus
// to the task's owner, listing the signals. A request already there (a
// repeated done) is not sent again.
func (b *Bus) RetroDue(ctx context.Context, task string) (Retro, error) {
	if IsRetroTask(task) {
		return Retro{}, nil
	}
	sigs, err := b.TaskFriction(ctx, task)
	if err != nil || len(sigs) == 0 {
		return Retro{}, err
	}
	owner, err := b.taskOwner(ctx, task)
	if err != nil {
		return Retro{}, err
	}
	return b.sendRetro(ctx, task, owner, sigs)
}

// RetroRequest is the person's retro of task (§3.3): its friction plus the
// person's own signal, sent to to (the owner, or master for an ended pop).
func (b *Bus) RetroRequest(ctx context.Context, task, to string) (Retro, error) {
	if IsRetroTask(task) {
		return Retro{}, fmt.Errorf("retro: %s is itself a retro", task)
	}
	sigs, err := b.TaskFriction(ctx, task)
	if err != nil {
		return Retro{}, err
	}
	sigs = append(sigs, FrictionSignal{Kind: FrictionPerson, ID: "-"})
	return b.sendRetro(ctx, task, to, sigs)
}

func (b *Bus) taskOwner(ctx context.Context, task string) (string, error) {
	raw, err := b.r.HGet(ctx, BoardKey(b.project), task).Result()
	if err != nil {
		return "", err
	}
	var e BoardEntry
	if err := json.Unmarshal([]byte(raw), &e); err != nil || e.Request == nil {
		return "", fmt.Errorf("retro: task %q is not a tracked request", task)
	}
	return e.Request.Target, nil
}

// RetroOwner is the task's owner: the target of its tracked request.
func (b *Bus) RetroOwner(ctx context.Context, task string) (string, error) {
	return b.taskOwner(ctx, task)
}

func (b *Bus) sendRetro(ctx context.Context, task, to string, sigs []FrictionSignal) (Retro, error) {
	name := RetroTaskName(task)
	out := Retro{Task: name, Signals: sigs, To: to}
	var lines []string
	for _, s := range sigs {
		lines = append(lines, "- "+s.String())
	}
	body := fmt.Sprintf(retroBodyPrefix(task)+"%s). Read its history with: agentbus retro evidence %s\n"+
		"Then file at most three proposals, or close it with: agentbus retro none %s \"<root cause>\"\nSignals:\n%s",
		FrictionSummary(sigs), task, task, strings.Join(lines, "\n"))
	_, err := b.RequestBy(ctx, name, SystemSender, to, "", body, 0, false)
	if err != nil && strings.Contains(err.Error(), "board task already exists") {
		owner, err := b.retroOf(ctx, name, task)
		if err != nil {
			return Retro{}, err
		}
		out.To = owner
		return out, nil
	}
	if err != nil {
		return Retro{}, err
	}
	out.Sent = true
	return out, nil
}

// retroOf checks that the board's name entry is task's retro, and returns
// its owner: a request from agentbus whose body, while the stream keeps it,
// names task. Anything else under that name is refused, never taken as the
// retro.
func (b *Bus) retroOf(ctx context.Context, name, task string) (string, error) {
	raw, err := b.r.HGet(ctx, BoardKey(b.project), name).Result()
	if err != nil {
		return "", err
	}
	var e BoardEntry
	if json.Unmarshal([]byte(raw), &e) != nil || e.Request == nil || e.Request.From != SystemSender {
		return "", fmt.Errorf("retro: board task %s exists and is not a retro", name)
	}
	msgs, err := b.r.XRange(ctx, StreamKey(b.project, "cmd"), e.Request.ID, e.Request.ID).Result()
	if err != nil {
		return "", err
	}
	if len(msgs) == 1 {
		body, _ := msgs[0].Values["command"].(string)
		if !strings.HasPrefix(body, retroBodyPrefix(task)) {
			return "", fmt.Errorf("retro: board task %s is the retro of another task", name)
		}
	}
	return e.Request.Target, nil
}

func retroBodyPrefix(task string) string { return "retro due for " + task + " (" }

// SignalText is what a signal's entry says, for retro evidence: a reject's
// review message, a CI hand-off's or an escalation's text, an event's kind
// and detail, read from the signal's own stream only (ids repeat across
// streams). "" for the person's signal; an error when the entry is gone from
// its capped stream.
func (b *Bus) SignalText(ctx context.Context, s FrictionSignal) (string, error) {
	var keys []string
	switch s.Stream {
	case SignalVerdicts:
		keys = []string{VerdictsKey(b.project)}
	case SignalCmd:
		keys = []string{StreamKey(b.project, "cmd")}
	case SignalEvents:
		keys = []string{EventsKey(b.project)}
	default:
		return "", nil
	}
	for _, k := range keys {
		msgs, err := b.r.XRange(ctx, k, s.ID, s.ID).Result()
		if err != nil {
			return "", err
		}
		if len(msgs) == 0 {
			continue
		}
		v := toStringMap(msgs[0].Values)
		switch {
		case s.Kind == FrictionReject:
			msg := v["full"]
			if msg == "" {
				msg = v["message"]
			}
			return fmt.Sprintf("%s rejected %s's %s: %s", v["reviewer"], v["author"], v["subject"], msg), nil
		case v["kind"] != "":
			return strings.TrimSpace(v["kind"] + " " + v["detail"]), nil
		default:
			return v["command"], nil
		}
	}
	return "", fmt.Errorf("%s %s: no longer in its stream", s.Kind, s.ID)
}
