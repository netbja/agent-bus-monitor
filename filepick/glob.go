package filepick

import "strings"

// MatchGlob says whether rel (a file, slash-separated, relative to the
// repository root) matches glob, read as one root .gitignore line by this
// package's matcher: a name without a slash at any depth, dir/** what is
// inside dir, ** any depth of directories. A negation, an empty or an
// unreadable pattern never matches. agentbus deliver's small-change
// classifier relies on it (single-approval §3.2), so it is never small too.
func MatchGlob(glob, rel string) bool {
	return ignoreSet{}.with("", strings.TrimSpace(glob)).ignored(rel, false)
}
