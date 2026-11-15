package bus

import (
	"context"
	"encoding/json"
	"strings"
)

// What a held agent had (run-budget spec §4, login-updates r9 §3.6): when a
// hold begins for an agent with work, the dispatcher writes one field of
// {p}:held, agent → HeldRecord, fenced; its release deletes it, and so does
// retire --forget.

// HeldKey is the {p}:held hash.
func HeldKey(project string) string { return project + ":held" }

// HeldRecord is what a held agent had when its hold began.
type HeldRecord struct {
	Provider string   `json:"provider"`           // the held provider, or "login" for a login hold
	Since    int64    `json:"since"`              // the hold's start, Unix ms (Redis TIME)
	Tasks    []string `json:"tasks,omitempty"`    // its accepted tasks (board)
	Branches []string `json:"branches,omitempty"` // their branch, where the board has one
	Report   string   `json:"report,omitempty"`   // the id of its last report
}

// The reasons HeldReason returns.
const (
	HeldQuota = "quota" // hold:<provider>: its provider is at its limit
	HeldLogin = "login" // hold-login:<agent>: its own login has expired
)

// LoginHoldField is the {p}:quota field of an agent held because its login
// expired; its value is the expiry (Unix ms).
func LoginHoldField(agent string) string { return "hold-login:" + agent }

// HeldReason is why agent, on provider, is held by the {p}:quota marks:
// HeldQuota, HeldLogin, or "" when it is not. Every reader that excuses a
// held agent asks it, so it learns both holds.
func HeldReason(marks map[string]string, agent, provider string) string {
	if provider != "" && marks["hold:"+provider] != "" {
		return HeldQuota
	}
	if agent != "" && marks[LoginHoldField(agent)] != "" {
		return HeldLogin
	}
	return ""
}

// LoginHeldAgents are the agents with a login hold in marks.
func LoginHeldAgents(marks map[string]string) map[string]bool {
	out := map[string]bool{}
	for k, v := range marks {
		if a, ok := strings.CutPrefix(k, "hold-login:"); ok && a != "" && v != "" {
			out[a] = true
		}
	}
	return out
}

// FencedPutHeld writes agent's {p}:held field under the dispatch lease.
func (b *Bus) FencedPutHeld(ctx context.Context, token, agent string, rec HeldRecord) error {
	v, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return b.fencedHash(ctx, quotaHSet, token, HeldKey(b.project), agent, string(v))
}

// FencedDelHeld deletes agent's {p}:held field under the dispatch lease.
func (b *Bus) FencedDelHeld(ctx context.Context, token, agent string) error {
	return b.fencedHash(ctx, quotaHDel, token, HeldKey(b.project), agent)
}

// Held reads {p}:held; an unreadable field is skipped.
func (b *Bus) Held(ctx context.Context) (map[string]HeldRecord, error) {
	m, err := b.r.HGetAll(ctx, HeldKey(b.project)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]HeldRecord, len(m))
	for a, raw := range m {
		var rec HeldRecord
		if json.Unmarshal([]byte(raw), &rec) == nil {
			out[a] = rec
		}
	}
	return out, nil
}
