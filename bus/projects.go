// Cross-project discovery. Everything else in this package is scoped to one
// project through a *Bus; this file answers the question you have BEFORE you
// know a project name — "what is on this broker at all?" — which is why Projects
// is a package function taking a raw client rather than a *Bus method (Open
// already demands a valid project, so a method here would be a circle).
package bus

import (
	"context"
	"sort"

	"github.com/redis/go-redis/v9"
)

// projectKinds are the key suffixes that prove a project exists on this broker.
// A key whose suffix is not in this set belongs to someone else (the broker can
// be shared) and is ignored.
//
// The two-colon keys need no special case: {p}:gate:{agent} and {p}:armed:{agent}
// split to a "project" that still contains a colon, which ValidName rejects.
var projectKinds = map[string]bool{
	"status": true, "report": true, "notify": true, "cmd": true,
	"agents": true, "usage": true, "budget": true, "verdicts": true, "pilot": true,
}

// activityKinds are the streams whose newest entry dates a project.
var activityKinds = []string{"status", "report", "notify", "cmd"}

// ProjectSummary is one project's footprint on the broker, as `busmon --list`
// shows it. Agents counts every agent the project has ever seen (the :agents
// hash is never pruned), so a long-dead peer still counts; LastTS is what
// carries recency.
type ProjectSummary struct {
	Project string `json:"project"`
	Agents  int    `json:"agents"`
	Master  string `json:"master,omitempty"` // pilot-lease driver; "" when autonomous
	LastTS  int64  `json:"last_ts"`          // ms since epoch; 0 when nothing is dated
}

// Projects discovers every project on the broker, newest activity first. It is
// read-only and tolerates a shared broker: keys it does not recognise are
// skipped rather than guessed at.
func Projects(ctx context.Context, r *redis.Client) ([]ProjectSummary, error) {
	names, err := scanProjects(ctx, r)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectSummary, 0, len(names))
	for _, name := range names {
		b, err := Open(r, name)
		if err != nil {
			continue // scanProjects already applied ValidName; belt and braces
		}
		s, err := b.summary(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	sortProjects(out)
	return out, nil
}

// summary collects one project's row.
func (b *Bus) summary(ctx context.Context) (ProjectSummary, error) {
	s := ProjectSummary{Project: b.project}

	agents, err := b.Agents(ctx)
	if err != nil {
		return s, err
	}
	s.Agents = len(agents)
	// The :agents hash outlives a `busmon --reset` (XTRIM empties the streams but
	// leaves the hash), so it dates a purged project that the streams no longer can.
	for _, a := range agents {
		if a.TS > s.LastTS {
			s.LastTS = a.TS
		}
	}

	ts, err := b.lastStreamTS(ctx)
	if err != nil {
		return s, err
	}
	if ts > s.LastTS {
		s.LastTS = ts
	}

	driver, err := b.PilotDriver(ctx)
	if err != nil {
		return s, err
	}
	s.Master = driver
	return s, nil
}

// lastStreamTS is the ms timestamp of the newest entry across the project's four
// activity streams, or 0 when they are all empty or trimmed away.
func (b *Bus) lastStreamTS(ctx context.Context) (int64, error) {
	var newest int64
	for _, kind := range activityKinds {
		msgs, err := b.r.XRevRangeN(ctx, StreamKey(b.project, kind), "+", "-", 1).Result()
		if err != nil {
			return 0, err
		}
		if len(msgs) == 0 {
			continue
		}
		if ms, _ := splitID(msgs[0].ID); ms > newest {
			newest = ms
		}
	}
	return newest, nil
}

// scanProjects SCANs the keyspace and returns the distinct project names it can
// attribute a key to. SCAN (not KEYS) because this runs against a live broker.
func scanProjects(ctx context.Context, r *redis.Client) ([]string, error) {
	seen := map[string]bool{}
	var cursor uint64
	for {
		keys, next, err := r.Scan(ctx, cursor, "*", 500).Result()
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			if project, ok := projectFromKey(k); ok {
				seen[project] = true
			}
		}
		if cursor = next; cursor == 0 {
			break
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	return names, nil
}

// projectFromKey maps a Redis key to the project owning it; ok is false when the
// key is not part of any project's footprint.
func projectFromKey(key string) (string, bool) {
	project, kind := splitStreamKey(key)
	if !projectKinds[kind] || !ValidName(project) {
		return "", false
	}
	return project, true
}

// sortProjects orders newest-activity first — the project you just left is the
// one you are most likely looking for — with the name as a stable tiebreak.
func sortProjects(list []ProjectSummary) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].LastTS != list[j].LastTS {
			return list[i].LastTS > list[j].LastTS
		}
		return list[i].Project < list[j].Project
	})
}
