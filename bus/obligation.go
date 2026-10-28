package bus

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultHorizon is the age beyond which a cmd entry never triggers a wake on
// its own (spec section 3.1).
const DefaultHorizon = 24 * time.Hour

// ServerTime is Redis TIME. Anything compared across restarts uses it, never
// the local clock.
func (b *Bus) ServerTime(ctx context.Context) (time.Time, error) {
	return b.r.Time(ctx).Result()
}

// EnsureGroup creates the agent's consumer group on {p}:cmd at the horizon
// floor (<nowMs-horizonMs>-0), so a command sent before the agent first
// subscribed is still inside its group. An existing group is left untouched.
func (b *Bus) EnsureGroup(ctx context.Context, agent string, horizon time.Duration) error {
	if !ValidName(agent) {
		return fmt.Errorf("invalid agent %q", agent)
	}
	now, err := b.ServerTime(ctx)
	if err != nil {
		return err
	}
	floorMs := now.UnixMilli() - horizon.Milliseconds()
	if floorMs < 0 {
		floorMs = 0
	}
	err = b.r.XGroupCreateMkStream(ctx, StreamKey(b.project, "cmd"), agent, strconv.FormatInt(floorMs, 10)+"-0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

// Armed reports whether a subscriber currently holds the agent's receiver lease.
func (b *Bus) Armed(ctx context.Context, agent string) (bool, error) {
	if !ValidName(agent) {
		return false, fmt.Errorf("invalid agent %q", agent)
	}
	n, err := b.r.Exists(ctx, ReceiverKey(b.project, agent)).Result()
	return n > 0, err
}

// GroupNames lists the consumer groups of {p}:cmd. A missing stream yields nil.
func (b *Bus) GroupNames(ctx context.Context) ([]string, error) {
	groups, err := b.r.XInfoGroups(ctx, StreamKey(b.project, "cmd")).Result()
	if err != nil {
		if strings.Contains(err.Error(), "no such key") {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(groups))
	for _, g := range groups {
		names = append(names, g.Name)
	}
	sort.Strings(names)
	return names, nil
}

// CmdTargets returns the distinct valid targets present in the retained
// {p}:cmd stream (capped near streamMaxLen, so a full XRANGE is cheap).
func (b *Bus) CmdTargets(ctx context.Context) ([]string, error) {
	targets, _, err := b.CmdTargetsAndHumanSenders(ctx)
	return targets, err
}

// CmdTargetsAndHumanSenders returns the distinct valid command targets and
// the sender names on retained human=yes commands. The dispatcher uses the
// latter to avoid treating a person's identity as a missing agent.
func (b *Bus) CmdTargetsAndHumanSenders(ctx context.Context) (targets, humanSenders []string, err error) {
	msgs, err := b.r.XRange(ctx, StreamKey(b.project, "cmd"), "-", "+").Result()
	if err != nil {
		return nil, nil, err
	}
	seenTargets, seenHumans := map[string]bool{}, map[string]bool{}
	for _, m := range msgs {
		t, _ := m.Values["target"].(string)
		if t != PeopleTarget && ValidName(t) {
			seenTargets[t] = true
		}
		if m.Values["human"] == "yes" {
			from, _ := m.Values["from"].(string)
			if ValidName(from) {
				seenHumans[from] = true
			}
		}
	}
	if len(seenTargets) > 0 {
		targets = make([]string, 0, len(seenTargets))
		for name := range seenTargets {
			targets = append(targets, name)
		}
	}
	if len(seenHumans) > 0 {
		humanSenders = make([]string, 0, len(seenHumans))
		for name := range seenHumans {
			humanSenders = append(humanSenders, name)
		}
	}
	sort.Strings(targets)
	sort.Strings(humanSenders)
	return targets, humanSenders, nil
}

// Obligation is what an agent still owes the bus: commands addressed to it
// that are worth waking it for.
type Obligation struct {
	GroupExists   bool
	Pending       int64  // PEL entries that are wake-relevant
	PendingTotal  int64  // raw XPENDING count, for progress
	Unread        int    // wake-relevant entries after the group's cursor
	Stale         int    // addressed, wake-eligible entries older than the horizon (not counted above)
	GapPossible   bool   // entries may have been trimmed before the agent read them
	OldestID      string // oldest wake-relevant id (PEL + unread)
	HighID        string // newest wake-relevant id
	LastDelivered string
	PendingIDs    []string // every entry in the group's pending list (busmon's Requests: delivered, not acknowledged)
}

// Wakes reports whether anything is worth waking the agent for.
func (o Obligation) Wakes() bool { return o.Pending+int64(o.Unread) > 0 }

// Size is the number of wake-relevant messages.
func (o Obligation) Size() int64 { return o.Pending + int64(o.Unread) }

// Obligation computes the agent's obligation. Wake-relevant means addressed to
// agent, not wake=no, and not older than horizon (server time).
func (b *Bus) Obligation(ctx context.Context, agent string, horizon time.Duration) (Obligation, error) {
	var o Obligation
	if !ValidName(agent) {
		return o, fmt.Errorf("invalid agent %q", agent)
	}
	now, err := b.ServerTime(ctx)
	if err != nil {
		return o, err
	}
	cutoff := now.UnixMilli() - horizon.Milliseconds()
	stream := StreamKey(b.project, "cmd")

	stale := func(id string) bool { ms, _ := splitID(id); return ms < cutoff }
	relevant := func(v map[string]interface{}) bool {
		f := toStringMap(v)
		return f["target"] == agent && f["wake"] != "no"
	}
	note := func(id string) {
		if o.OldestID == "" || idLess(id, o.OldestID) {
			o.OldestID = id
		}
		if o.HighID == "" || idLess(o.HighID, id) {
			o.HighID = id
		}
	}

	var groupInfo *redis.XInfoGroup
	groups, err := b.r.XInfoGroups(ctx, stream).Result()
	if err != nil && !strings.Contains(err.Error(), "no such key") {
		return o, err
	}
	for i := range groups {
		if groups[i].Name == agent {
			groupInfo = &groups[i]
		}
	}

	from := "-"
	if groupInfo != nil {
		o.GroupExists = true
		o.LastDelivered = groupInfo.LastDeliveredID
		from = "(" + groupInfo.LastDeliveredID

		// Pending entries: ids from XPENDING, bodies in one pipeline.
		pend, err := b.r.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: stream, Group: agent, Start: "-", End: "+", Count: 1000}).Result()
		if err != nil {
			return o, err
		}
		o.PendingTotal = groupInfo.Pending
		if n := int64(len(pend)); n > o.PendingTotal {
			o.PendingTotal = n
		}
		pipe := b.r.Pipeline()
		cmds := make([]*redis.XMessageSliceCmd, len(pend))
		for i, p := range pend {
			cmds[i] = pipe.XRange(ctx, stream, p.ID, p.ID)
			o.PendingIDs = append(o.PendingIDs, p.ID)
		}
		if len(pend) > 0 {
			if _, err := pipe.Exec(ctx); err != nil {
				return o, err
			}
		}
		for i, p := range pend {
			msgs, _ := cmds[i].Result()
			if len(msgs) == 0 {
				// Trimmed body: unknowable, so it counts unless provably stale.
				if stale(p.ID) {
					o.Stale++
				} else {
					o.Pending++
					note(p.ID)
				}
				continue
			}
			if !relevant(msgs[0].Values) {
				continue
			}
			if stale(p.ID) {
				o.Stale++
				continue
			}
			o.Pending++
			note(p.ID)
		}
	}

	msgs, err := b.r.XRange(ctx, stream, from, "+").Result()
	if err != nil && !strings.Contains(err.Error(), "no such key") {
		return o, err
	}
	for _, m := range msgs {
		if !relevant(m.Values) {
			continue
		}
		if stale(m.ID) {
			o.Stale++
			continue
		}
		o.Unread++
		note(m.ID)
	}

	if groupInfo != nil {
		o.GapPossible = b.gapPossible(ctx, stream, groupInfo.LastDeliveredID)
	}
	return o, nil
}

// gapPossible is deliberately conservative: it is true only when the group's
// cursor is older than the oldest retained entry (or the stream is empty past
// the cursor) AND the stream has dropped entries (entries-added > length).
// A group at "0-0" on a stream that was never trimmed is therefore false.
// An XINFO failure yields false: no evidence, no gap claim.
func (b *Bus) gapPossible(ctx context.Context, stream, lastDelivered string) bool {
	info, err := b.r.XInfoStream(ctx, stream).Result()
	if err != nil || info.EntriesAdded <= info.Length {
		return false
	}
	if info.Length == 0 {
		return idLess(lastDelivered, info.LastGeneratedID)
	}
	return idLess(lastDelivered, info.FirstEntry.ID)
}
