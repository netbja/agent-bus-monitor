package filepick

import (
	"path"
	"slices"
	"strings"
)

// Rank orders the entries a query matches (§4.2), case-insensitively: the
// file name starts with it, then a path segment does, then the path contains
// it, then its runes appear in order in the path; ties go to the shorter
// path, then alphabetical order. A query with a / matches the path from its
// start. An empty query lists recent (the person's picks this session, most
// recent first) that are still listed, then the top-level entries.
func Rank(entries []Entry, query string, recent []string) []Entry {
	q := strings.ToLower(query)
	var out []Entry
	if q == "" {
		byPath := map[string]Entry{}
		for _, e := range entries {
			byPath[e.Path] = e
		}
		seen := map[string]bool{}
		for _, r := range recent {
			if e, ok := byPath[r]; ok && !seen[r] {
				out, seen[r] = append(out, e), true
			}
		}
		var top []Entry
		for _, e := range entries {
			if !strings.Contains(e.Path, "/") && !seen[e.Path] {
				top = append(top, e)
			}
		}
		slices.SortFunc(top, func(a, b Entry) int { return strings.Compare(strings.ToLower(a.Path), strings.ToLower(b.Path)) })
		return append(out, top...)
	}
	type ranked struct {
		e    Entry
		tier int
	}
	var rs []ranked
	for _, e := range entries {
		if t, ok := tier(strings.ToLower(e.Path), q); ok {
			rs = append(rs, ranked{e, t})
		}
	}
	slices.SortFunc(rs, func(a, b ranked) int {
		switch {
		case a.tier != b.tier:
			return a.tier - b.tier
		case len(a.e.Path) != len(b.e.Path):
			return len(a.e.Path) - len(b.e.Path)
		}
		return strings.Compare(a.e.Path, b.e.Path)
	})
	for _, r := range rs {
		out = append(out, r.e)
	}
	return out
}

// tier is how well p (lower case) matches q (lower case), lower is better.
func tier(p, q string) (int, bool) {
	if strings.Contains(q, "/") {
		return 0, strings.HasPrefix(p, q)
	}
	switch {
	case strings.HasPrefix(path.Base(p), q):
		return 0, true
	case slices.ContainsFunc(strings.Split(p, "/"), func(s string) bool { return strings.HasPrefix(s, q) }):
		return 1, true
	case strings.Contains(p, q):
		return 2, true
	}
	rest := []rune(q)
	for _, r := range p {
		if len(rest) > 0 && r == rest[0] {
			rest = rest[1:]
		}
	}
	return 3, len(rest) == 0
}
