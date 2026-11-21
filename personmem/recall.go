package personmem

import (
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Query is a recall (spec §4.2): words, filters, and how many.
type Query struct {
	Words   string
	Type    string
	Project string // the project it was said in; observations said in none (busmon) always match
	All     bool   // superseded ones too
	Limit   int    // 0: every match
}

// Hit is a recalled observation and its topic.
type Hit struct {
	Topic string
	Observation
}

// Recall ranks the observations for q as spec §3 says, without an index: an
// exact topic, a partial topic, the type, then the words in the quote or the
// question; ties go to importance, then the newest. Superseded observations
// are left out unless q.All. No words: every observation, by importance
// then the newest.
func (s *Store) Recall(q Query) []Hit {
	all, _ := s.loadAll()
	gone := superseded(all)
	words := strings.Fields(strings.ToLower(q.Words))
	phrase := strings.Join(words, " ")
	type ranked struct {
		Hit
		tier int
	}
	var out []ranked
	for _, t := range all {
		for _, o := range t.Obs {
			if !q.All && gone[o.ID] || q.Type != "" && o.Type != q.Type {
				continue
			}
			if p := saidProject(o.Said); q.Project != "" && p != "" && p != q.Project {
				continue
			}
			tier := matchTier(t.Topic, o, phrase, words)
			if tier < 0 {
				continue
			}
			out = append(out, ranked{Hit{t.Topic, o}, tier})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if ia, ib := slices.Index(Importances, a.Importance), slices.Index(Importances, b.Importance); ia != ib {
			return ia < ib
		}
		if na, nb := newness(a.ID), newness(b.ID); na != nb {
			return na > nb
		}
		return a.ID < b.ID
	})
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	hits := make([]Hit, len(out))
	for i, r := range out {
		hits[i] = r.Hit
	}
	return hits
}

// matchTier is 0 an exact topic, 1 a partial topic, 2 the type, 3 every
// word in the quote or the question; -1 no match. No words: 0 for all.
func matchTier(topic string, o Observation, phrase string, words []string) int {
	switch {
	case phrase == "" || topic == phrase:
		return 0
	case strings.Contains(topic, phrase):
		return 1
	case o.Type == phrase:
		return 2
	}
	text := strings.ToLower(o.Quote + " " + o.Question)
	for _, w := range words {
		if !strings.Contains(text, w) {
			return -1
		}
	}
	return 3
}

// newness orders observations by when they were said: the ms of their
// entry (or of busmon's write), from the id.
func newness(id string) int64 {
	_, rest, _ := strings.Cut(id, "-")
	rest = strings.TrimPrefix(rest, "b")
	ms, _, _ := strings.Cut(rest, "-")
	n, _ := strconv.ParseInt(ms, 10, 64)
	return n
}

// saidProject is the project a said line names ("" from busmon).
func saidProject(said string) string {
	_, rest, ok := strings.Cut(said, ", project ")
	if !ok {
		return ""
	}
	p, _, _ := strings.Cut(rest, ",")
	return p
}
