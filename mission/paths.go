package mission

import (
	"path"
	"strings"
)

// MatchPath matches a slash path against an allowed_paths pattern: path.Match
// segment by segment, and a final "/**" that matches anything below.
func MatchPath(pattern, p string) bool {
	if dir, ok := strings.CutSuffix(pattern, "/**"); ok {
		ps, ds := strings.Split(p, "/"), strings.Split(dir, "/")
		if len(ps) <= len(ds) {
			return false
		}
		return matchSegments(ds, ps[:len(ds)])
	}
	ps, qs := strings.Split(p, "/"), strings.Split(pattern, "/")
	return len(ps) == len(qs) && matchSegments(qs, ps)
}

func matchSegments(pattern, p []string) bool {
	for i := range pattern {
		if ok, err := path.Match(pattern[i], p[i]); err != nil || !ok {
			return false
		}
	}
	return true
}

// Overlap reports whether two missions' allowed_paths may touch the same file:
// a file of the tree matches both, or (for files not created yet) one
// pattern's literal prefix is a prefix of the other's. Deliberately
// conservative: a false positive costs parallelism, never correctness.
func Overlap(a, b, tree []string) bool {
	for _, f := range tree {
		if matchAny(a, f) && matchAny(b, f) {
			return true
		}
	}
	for _, p := range a {
		for _, q := range b {
			lp, lq := literalPrefix(p), literalPrefix(q)
			if strings.HasPrefix(lp, lq) || strings.HasPrefix(lq, lp) {
				return true
			}
		}
	}
	return false
}

func matchAny(patterns []string, p string) bool {
	for _, q := range patterns {
		if MatchPath(q, p) {
			return true
		}
	}
	return false
}

// literalPrefix is the pattern up to its first wildcard.
func literalPrefix(p string) string {
	if i := strings.IndexAny(p, "*?["); i >= 0 {
		return p[:i]
	}
	return p
}
