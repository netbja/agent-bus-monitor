package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// mergeSnapshot computes the {p}:agents entry a write leaves behind (v0.10.1).
// A bind (identityOnly) sets only the identity. A status sets state, message
// and ts unless it is older than the stored entry, and replaces the identity
// only when it verified one itself: an unverified caller's env pane is not
// trusted over a pid that was proved (Codex's app-server inherits another
// session's HERDR_PANE_ID).
func mergeSnapshot(old *AgentSnapshot, next AgentSnapshot, identityOnly bool) AgentSnapshot {
	var out AgentSnapshot
	if old != nil {
		out = *old
	}
	setIdentity := func() {
		out.Pane, out.PID, out.PIDStart, out.Terminal = next.Pane, next.PID, next.PIDStart, next.Terminal
	}
	if identityOnly {
		setIdentity()
		return out
	}
	if old == nil || next.TS >= old.TS {
		out.State, out.Message, out.TS = next.State, next.Message, next.TS
	}
	if next.Session != "" {
		out.Session = next.Session
	}
	if next.PID > 0 || old == nil || old.PID == 0 {
		setIdentity()
	}
	return out
}

// putSnapshot applies mergeSnapshot to the stored entry in one optimistic
// transaction, so a bind and a status racing on the same agent never lose
// each other's fields.
func (b *Bus) putSnapshot(ctx context.Context, agent string, next AgentSnapshot, identityOnly bool) error {
	key := AgentsKey(b.project)
	txf := func(tx *redis.Tx) error {
		var old *AgentSnapshot
		raw, err := tx.HGet(ctx, key, agent).Result()
		switch {
		case err == redis.Nil:
		case err != nil:
			return err
		default:
			var s AgentSnapshot
			if json.Unmarshal([]byte(raw), &s) == nil {
				old = &s
			}
		}
		snap, err := json.Marshal(mergeSnapshot(old, next, identityOnly))
		if err != nil {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.HSet(ctx, key, agent, snap)
			return nil
		})
		return err
	}
	for i := 0; i < 10; i++ {
		err := b.r.Watch(ctx, txf, key)
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
	}
	return redis.TxFailedErr
}

// Bind records a verified identity for agent (pane, pid, pid start time,
// terminal) without touching its declared state. The caller has checked the
// pid against the pane's foreground set; Bind refuses an identity with no pid.
func (b *Bus) Bind(ctx context.Context, agent string, who AgentIdent) error {
	if !ValidName(agent) {
		return fmt.Errorf("invalid agent %q", agent)
	}
	if who.PID <= 0 || who.PIDStart == 0 || who.Pane == "" {
		return fmt.Errorf("bind needs a pane, a pid and its start time")
	}
	return b.putSnapshot(ctx, agent, AgentSnapshot{Pane: who.Pane, PID: who.PID, PIDStart: who.PIDStart, Terminal: who.Terminal}, true)
}
