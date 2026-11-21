package config

import (
	"regexp"
	"strings"
)

func isForge(s string) bool {
	return s == "auto" || s == "github" || s == "gitlab" || s == "bitbucket" || s == "none" || s == "local"
}

// ValidBranchName is git check-ref-format --branch's rule for a branch name,
// without running git: no leading dash, no component starting with a dot or
// ending in .lock, no "..", "@{", "//", control, space, or ~^:?*[\, not
// "@", and no leading or trailing slash or trailing dot.
func ValidBranchName(s string) bool {
	if s == "" || s == "@" || s == "HEAD" || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") ||
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

// IsBrokerRuntime: the container runtimes broker.runtime accepts.
func IsBrokerRuntime(s string) bool { return s == "docker" || s == "podman" }

// imageRef is a container image reference: a name of lowercase path
// components (an optional registry host with a port), an optional tag.
var imageRef = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(:[0-9]+)?(/[a-z0-9]+([._-][a-z0-9]+)*)*(:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?$`)

// IsImageRef: broker.image's values (redis:8-alpine, valkey/valkey:8, a
// registry's own); nothing a shell or compose would read as more.
func IsImageRef(s string) bool { return len(s) <= 255 && imageRef.MatchString(s) }
