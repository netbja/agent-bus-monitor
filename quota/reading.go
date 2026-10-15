// Package quota reads how much of each AI provider's usage windows is spent,
// from first-party local sources, with no model call (spec
// docs/superpowers/specs/2026-10-15-quota-awareness-design.md). Every reader
// returns the same Reading; the policy and busmon know nothing else.
package quota

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// Window is one usage window of a provider: used percent (0-100), when it
// resets, and its length when known.
type Window struct {
	Name     string  `json:"name"`
	UsedPct  float64 `json:"used_pct"`
	ResetsAt int64   `json:"resets_at"` // Unix seconds
	Minutes  int     `json:"minutes,omitempty"`
}

// Reset is ResetsAt as a time.
func (w Window) Reset() time.Time { return time.Unix(w.ResetsAt, 0) }

// Reading is what one reader knows about one provider (spec §4).
type Reading struct {
	Provider    string   `json:"provider"`
	Account     string   `json:"account,omitempty"`
	Windows     []Window `json:"windows,omitempty"`
	ObservedAt  int64    `json:"observed_at,omitempty"`  // Unix ms: when the numbers were true
	CollectedAt int64    `json:"collected_at,omitempty"` // Unix ms: when we read them
	Source      string   `json:"source,omitempty"`
	Unlimited   bool     `json:"unlimited,omitempty"`
	Unknown     bool     `json:"unknown,omitempty"`
	Note        string   `json:"note,omitempty"` // why unknown, for agentbus quota; never source text
}

// Reader is one provider's source.
type Reader interface {
	Provider() string
	Read(ctx context.Context) (Reading, error)
}

// Unknown is the reading of a provider whose source said nothing usable.
func Unknown(provider, source, why string) Reading {
	return Reading{Provider: provider, Source: source, Unknown: true, Note: why}
}

// Validate checks a reading against the contract; a reading that fails it is
// unknown, never partially trusted.
func (r Reading) Validate() error {
	if r.Provider == "" {
		return fmt.Errorf("no provider")
	}
	if r.Unknown || r.Unlimited {
		return nil
	}
	if len(r.Windows) == 0 {
		return fmt.Errorf("no window")
	}
	for _, w := range r.Windows {
		if w.Name == "" {
			return fmt.Errorf("a window has no name")
		}
		if math.IsNaN(w.UsedPct) || w.UsedPct < 0 || w.UsedPct > 1000 {
			return fmt.Errorf("window %s: used_pct %v out of range", w.Name, w.UsedPct)
		}
		if w.ResetsAt <= 0 {
			return fmt.Errorf("window %s: no reset time", w.Name)
		}
	}
	return nil
}

// Usable is the windows the policy may act on at now: the reading is known,
// observed within maxAge, and each window's reset is still ahead (spec §7.1).
// Display keeps the others, aged.
func (r Reading) Usable(now time.Time, maxAge time.Duration) []Window {
	if r.Unknown || r.Unlimited || r.ObservedAt == 0 || now.Sub(time.UnixMilli(r.ObservedAt)) > maxAge {
		return nil
	}
	var out []Window
	for _, w := range r.Windows {
		if w.Reset().After(now) {
			out = append(out, w)
		}
	}
	return out
}

// Tightest is the window closest to its limit (highest used percent; the
// earlier reset breaks a tie). ok is false for none.
func Tightest(ws []Window) (Window, bool) {
	if len(ws) == 0 {
		return Window{}, false
	}
	s := append([]Window(nil), ws...)
	sort.SliceStable(s, func(i, j int) bool {
		if s[i].UsedPct != s[j].UsedPct {
			return s[i].UsedPct > s[j].UsedPct
		}
		return s[i].ResetsAt < s[j].ResetsAt
	})
	return s[0], true
}

// WindowName is the canonical name for a window length in minutes.
func WindowName(minutes int) string {
	switch minutes {
	case 300:
		return "5h"
	case 1440:
		return "daily"
	case 10080:
		return "7d"
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return "window"
}
