package filepick

import (
	"path"
	"strings"
)

// The ignore rules (compose-at-file §2.1): the .gitignore files met on the
// way and .git/info/exclude, read as text by this small matcher. Reading a
// file never runs anything; git's global excludes file needs git's config,
// so it is not read (a few more files may show).

// ignoreRule is one line of a .gitignore, relative to the directory that
// holds it.
type ignoreRule struct {
	base     string   // the .gitignore's directory, relative to the root ("" for the root)
	segs     []string // the pattern's segments
	neg      bool     // !pattern: back in
	dirOnly  bool     // pattern/: directories only
	anchored bool     // a slash before the end: from base, else a name at any depth
}

// ignoreSet is the rules in force, in the order they were met: the last
// that matches wins, as in git.
type ignoreSet []ignoreRule

// with is s plus the rules of a .gitignore at base (a new slice: a sibling
// directory never sees another's rules).
func (s ignoreSet) with(base, text string) ignoreSet {
	out := append(ignoreSet(nil), s...)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasSuffix(line, "\\ ") {
			line = strings.TrimRight(line, " \t")
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := ignoreRule{base: base}
		switch {
		case strings.HasPrefix(line, "\\#"), strings.HasPrefix(line, "\\!"):
			line = line[1:]
		case strings.HasPrefix(line, "!"):
			r.neg, line = true, line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly, line = true, strings.TrimRight(line, "/")
		}
		if line == "" {
			continue
		}
		r.anchored = strings.Contains(line, "/")
		line = strings.TrimPrefix(line, "/")
		for _, seg := range strings.Split(line, "/") {
			if seg != "" {
				r.segs = append(r.segs, seg)
			}
		}
		if len(r.segs) == 0 || !validSegs(r.segs) {
			continue // a pattern path.Match cannot read never matches, as git skips it
		}
		out = append(out, r)
	}
	return out
}

func validSegs(segs []string) bool {
	for _, s := range segs {
		if _, err := path.Match(s, ""); err != nil {
			return false
		}
	}
	return true
}

// ignored says whether rel (slash-separated, relative to the root) is
// ignored; dir says it is a directory.
func (s ignoreSet) ignored(rel string, dir bool) bool {
	out := false
	for _, r := range s {
		if r.matches(rel, dir) {
			out = !r.neg
		}
	}
	return out
}

func (r ignoreRule) matches(rel string, dir bool) bool {
	if r.dirOnly && !dir {
		return false
	}
	sub := rel
	if r.base != "" {
		if !strings.HasPrefix(rel, r.base+"/") {
			return false
		}
		sub = rel[len(r.base)+1:]
	}
	segs := strings.Split(sub, "/")
	if !r.anchored { // a name, at any depth below base
		ok, _ := path.Match(r.segs[0], segs[len(segs)-1])
		return ok
	}
	return matchSegs(r.segs, segs)
}

// matchSegs matches path segments, ** standing for zero or more of them;
// a trailing ** stands for one or more (logs/** is what is inside logs, not
// logs itself).
func matchSegs(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		if len(pat) == 1 {
			return len(segs) > 0
		}
		for i := 0; i <= len(segs); i++ {
			if matchSegs(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	ok, _ := path.Match(pat[0], segs[0])
	return ok && matchSegs(pat[1:], segs[1:])
}
