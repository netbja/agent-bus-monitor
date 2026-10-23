package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/redis/go-redis/v9"
)

// mergeSnapshot computes the {p}:agents entry a write leaves behind (v0.10.1).
// A bind (identityOnly, next.IdentTS = Redis time) sets only the identity. A
// status sets state, message and ts unless it is older than the stored entry.
// It replaces the identity only when it verified one itself and that proof is
// not older than the stored one (a delayed status must not undo a later bind);
// an unverified caller's env pane is never trusted over a proved pid (Codex's
// app-server inherits another session's HERDR_PANE_ID).
func mergeSnapshot(old *AgentSnapshot, next AgentSnapshot, identityOnly bool) AgentSnapshot {
	var out AgentSnapshot
	if old != nil {
		out = *old
	}
	setIdentity := func(at int64) {
		// The launched model is part of a proved identity (v0.15.1): a proof for
		// the same process keeps it when the caller does not know it; a proof
		// for another process takes the caller's, even empty. Nothing unproved
		// (at == 0) touches it.
		if at != 0 && (next.Model != "" || next.PID != out.PID || next.PIDStart != out.PIDStart) {
			out.Model = next.Model
		}
		// The launched client's fingerprint follows the same rule.
		if at != 0 && (next.ClientFP != "" || next.PID != out.PID || next.PIDStart != out.PIDStart) {
			out.ClientFP = next.ClientFP
		}
		out.Pane, out.PID, out.PIDStart, out.Terminal, out.IdentTS = next.Pane, next.PID, next.PIDStart, next.Terminal, at
	}
	if next.Client != "" {
		out.Client = next.Client
	}
	if next.Provider != "" {
		out.Provider = next.Provider
	}
	if identityOnly {
		setIdentity(next.IdentTS)
		return out
	}
	if old == nil || next.TS >= old.TS {
		out.State, out.Message, out.TS = next.State, next.Message, next.TS
	}
	if next.Session != "" {
		out.Session = next.Session
	}
	switch {
	case next.PID > 0 && (old == nil || next.TS >= old.IdentTS):
		setIdentity(next.TS)
	case next.PID == 0 && (old == nil || old.PID == 0):
		setIdentity(0) // nothing proved either way: the env pane, as before
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
	// The hash holds every agent, so busy peers make the WATCH fail; back off a
	// little, with jitter, before trying again.
	for i := 0; i < 50; i++ {
		err := b.r.Watch(ctx, txf, key)
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(1+rand.IntN(5)) * time.Millisecond):
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
	now, err := b.ServerNowMS(ctx)
	if err != nil {
		return err
	}
	return b.putSnapshot(ctx, agent, AgentSnapshot{Pane: who.Pane, PID: who.PID, PIDStart: who.PIDStart, Terminal: who.Terminal, IdentTS: now,
		Client: who.Client, Provider: who.Provider, Model: who.Model, ClientFP: who.ClientFP}, true)
}
