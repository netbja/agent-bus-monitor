// Package usage reads budget and per-agent usage out of the local artefacts
// Claude Code (and its status line) already write — so nothing has to be teed by
// a cooperating agent. An agent that forgets to publish, or whose status-line
// script never had the tee wired in, is still measured.
//
// Two sources, two scopes:
//
//	ccstatusline cache  -> the ACCOUNT budget (session/weekly windows), one row per provider
//	transcript JSONL    -> ONE AGENT's model + context fill, exact and retroactive
package usage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// AnthropicProvider is the provider key for a Claude Code subscription.
const AnthropicProvider = "anthropic"

// ccstatuslineCache is the on-disk shape of ~/.cache/ccstatusline/usage.json.
// ccstatusline fetches it from Anthropic's OAuth usage endpoint and caches it
// here; reading the cache costs nothing and needs no credentials of our own.
// Unknown keys are ignored, so a ccstatusline upgrade that adds fields is safe.
type ccstatuslineCache struct {
	SessionUsage      float64 `json:"sessionUsage"`
	SessionResetAt    string  `json:"sessionResetAt"`
	WeeklyUsage       float64 `json:"weeklyUsage"`
	WeeklyResetAt     string  `json:"weeklyResetAt"`
	WeeklySonnetUsage float64 `json:"weeklySonnetUsage"`
	WeeklyOpusUsage   float64 `json:"weeklyOpusUsage"`
	ExtraUsageEnabled bool    `json:"extraUsageEnabled"`
}

// CCStatuslinePath is where ccstatusline caches the account budget.
func CCStatuslinePath() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "ccstatusline", "usage.json")
}

// ReadCCStatusline parses the ccstatusline cache into an account budget.
//
// The cache is only as fresh as the last status-line refresh — an agent that has
// been idle for an hour has an hour-old cache. Callers surface the snapshot's TS
// so a stale reading is visible as stale rather than silently trusted.
func ReadCCStatusline(path string) (bus.BudgetSnapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return bus.BudgetSnapshot{}, err
	}
	var c ccstatuslineCache
	if err := json.Unmarshal(raw, &c); err != nil {
		return bus.BudgetSnapshot{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	snap := bus.BudgetSnapshot{
		Provider:     AnthropicProvider,
		SessionPct:   c.SessionUsage,
		SessionReset: c.SessionResetAt,
		WeeklyPct:    c.WeeklyUsage,
		WeeklyReset:  c.WeeklyResetAt,
		Source:       "ccstatusline",
		TS:           time.Now().UnixMilli(),
	}
	// Per-model weekly splits ride in Extra: a provider-specific gauge that must
	// not force a schema change when the next provider reports something else.
	extra := map[string]float64{}
	if c.WeeklySonnetUsage != 0 {
		extra["weekly_sonnet_pct"] = c.WeeklySonnetUsage
	}
	if c.WeeklyOpusUsage != 0 {
		extra["weekly_opus_pct"] = c.WeeklyOpusUsage
	}
	if len(extra) > 0 {
		snap.Extra = extra
	}
	// mtime beats time.Now(): it says when the figures were actually fetched.
	if st, serr := os.Stat(path); serr == nil {
		snap.TS = st.ModTime().UnixMilli()
	}
	return snap, nil
}
