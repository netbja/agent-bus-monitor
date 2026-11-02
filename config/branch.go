package config

import "strings"

func isForge(s string) bool {
	return s == "auto" || s == "github" || s == "gitlab" || s == "bitbucket" || s == "local"
}

// ValidBranchName is git check-ref-format --branch's rule for a branch name,
// without running git: no leading dash, no component starting with a dot or
// ending in .lock, no "..", "@{", "//", control, space, or ~^:?*[\, not
// "@", and no leading or trailing slash or trailing dot.
func ValidBranchName(s string) bool {
	if s == "" || s == "@" || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") ||
		strings.HasSuffix(s, ".") || strings.Contains(s, "..") || strings.Contains(s, "@{") || strings.Contains(s, "//") {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			return false
		}
	}
	for _, part := range strings.Split(s, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}
