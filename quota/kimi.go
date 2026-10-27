package quota

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// KimiReader reads Kimi Code's own session logs for a live usage-limit hit
// (kimi-quota): `llm request failed … errorMessage="429 You've reached your
// 5-hour usage limit…"` (403 too). Kimi Code exposes no quota numbers, so
// this is a backstop like Claude's transcripts: a hit is that window at
// 100 %, and any later answer (`llm response`) voids it (AvailableAt).
//
// The quota is the account's, so every project's sessions count. Only
// <Root>/<wd>/<session>/logs/kimi-code.log is opened, for the kimiMaxLogs
// newest modified within the longest window, their last 256 KiB: never
// config.toml, credentials, device_id or anything else under ~/.kimi-code.
//
// Kimi gives no reset time. The reset is an upper bound derived from the
// window's length: the window exhausted at the episode's first hit (the
// first after the last answer) ends at most 5 h (or 7 days) after it. A
// billing-cycle limit has no stated length, so it is reported unknown, never
// held.
type KimiReader struct {
	Root string // ~/.kimi-code/sessions
	Now  func() time.Time
}

// kimiMaxLogs bounds the session logs read per collection.
const kimiMaxLogs = 8

// kimiWindows: the limit's words in the error message, its window.
var kimiWindows = []struct {
	words   string
	name    string
	minutes int
}{
	{"5-hour usage limit", "5h", 300},
	{"weekly (7-day) usage limit", "7d", 7 * 24 * 60},
}

type kimiHit struct {
	at      time.Time
	name    string
	minutes int
}

func (KimiReader) Provider() string { return "moonshot" }

func (k KimiReader) Read(ctx context.Context) (Reading, error) {
	now := k.Now()
	logs := kimiLogs(k.Root, now)
	if len(logs) == 0 {
		return Unknown("moonshot", "kimi-log", "no recent Kimi Code session log"), nil
	}
	var hits []kimiHit
	var served int64
	var billing time.Time
	for _, f := range logs {
		for _, line := range tailLines(f, 256<<10) {
			at, rest, ok := kimiLine(line)
			if !ok || at.After(now) {
				continue // not a log line, or written after now (a skewed clock)
			}
			switch {
			case bytes.HasPrefix(rest, []byte("INFO  llm response ")):
				if ms := at.UnixMilli(); ms > served {
					served = ms
				}
			case bytes.Contains(rest, []byte(" llm request failed ")) && bytes.Contains(rest, []byte("usage limit")):
				if bytes.Contains(rest, []byte("billing cycle")) {
					if at.After(billing) {
						billing = at
					}
					continue
				}
				for _, w := range kimiWindows {
					if bytes.Contains(rest, []byte(w.words)) {
						hits = append(hits, kimiHit{at, w.name, w.minutes})
						break
					}
				}
			}
		}
	}
	// Per window, the hits after the last answer: the first bounds the reset,
	// the newest is when the limit was last seen to hold.
	first, last := map[string]time.Time{}, map[string]time.Time{}
	minutes := map[string]int{}
	for _, h := range hits {
		if h.at.UnixMilli() <= served {
			continue // served since: the limit no longer stops it
		}
		if f, ok := first[h.name]; !ok || h.at.Before(f) {
			first[h.name] = h.at
		}
		if l, ok := last[h.name]; !ok || h.at.After(l) {
			last[h.name] = h.at
		}
		minutes[h.name] = h.minutes
	}
	r := Reading{Provider: "moonshot", Source: "kimi-log", CollectedAt: now.UnixMilli(), AvailableAt: served}
	names := make([]string, 0, len(first))
	for n := range first {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		reset := first[n].Add(time.Duration(minutes[n]) * time.Minute)
		if !reset.After(now) {
			continue // its window has ended
		}
		obs := last[n].UnixMilli()
		r.Windows = append(r.Windows, Window{Name: n, UsedPct: 100, ResetsAt: reset.Unix(), Minutes: minutes[n], ObservedAt: obs})
		if obs > r.ObservedAt {
			r.ObservedAt = obs
		}
	}
	if len(r.Windows) == 0 {
		why := "no usage-limit hit in recent Kimi Code logs"
		if billing.UnixMilli() > served {
			why = "usage limit for this billing cycle hit at " + billing.UTC().Format(time.RFC3339) + "; Kimi states no reset, so it is not held"
		}
		u := Unknown("moonshot", "kimi-log", why)
		u.AvailableAt = served
		return u, nil
	}
	return r, nil
}

// kimiLogs is the newest session logs modified within the longest window.
func kimiLogs(root string, now time.Time) []string {
	files, _ := filepath.Glob(filepath.Join(root, "*", "*", "logs", "kimi-code.log"))
	type f struct {
		path string
		mod  time.Time
	}
	var recent []f
	for _, p := range files {
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() || now.Sub(st.ModTime()) > 7*24*time.Hour {
			continue
		}
		recent = append(recent, f{p, st.ModTime()})
	}
	sort.Slice(recent, func(i, j int) bool { return recent[i].mod.After(recent[j].mod) })
	if len(recent) > kimiMaxLogs {
		recent = recent[:kimiMaxLogs]
	}
	out := make([]string, len(recent))
	for i, r := range recent {
		out[i] = r.path
	}
	return out
}

// kimiLine splits `<RFC 3339 UTC time> <rest>`.
func kimiLine(line []byte) (time.Time, []byte, bool) {
	sp := bytes.IndexByte(line, ' ')
	if sp <= 0 {
		return time.Time{}, nil, false
	}
	at, err := time.Parse(time.RFC3339Nano, string(line[:sp]))
	if err != nil {
		return time.Time{}, nil, false
	}
	return at, line[sp+1:], true
}
