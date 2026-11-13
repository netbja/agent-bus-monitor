package service

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// BuildKey is a build's key (herdr exit spec 4.4, security S7), the one name
// of a build in the holder's socket, the unit, the launchd label, the binary
// directory and the pidfiles: the raw build id lowered and reduced to
// [a-z0-9.] (any other run of characters becomes one '.', and the ends lose
// their dots; "build" when nothing is left), cut to 40 characters, then '.'
// and the first 10 hex digits of the raw id's SHA-256. Two raw ids that
// reduce alike ("+dirty" and "-dirty", upper case) differ by the hash. A key
// has no '-' and a project name (bus.ValidName) no '.', so "<project>.<key>"
// splits one way only. The raw id is recorded beside the key by its users.
func BuildKey(raw string) string {
	var b strings.Builder
	dot := false
	for _, r := range strings.ToLower(raw) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' {
			b.WriteRune(r)
			dot = false
			continue
		}
		if !dot {
			b.WriteByte('.')
			dot = true
		}
	}
	base := strings.Trim(b.String(), ".")
	if len(base) > 40 {
		base = strings.TrimRight(base[:40], ".")
	}
	if base == "" {
		base = "build"
	}
	sum := sha256.Sum256([]byte(raw))
	return base + "." + hex.EncodeToString(sum[:])[:10]
}

var buildKeyRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.]{0,39}\.[0-9a-f]{10}$`)

// ValidBuildKey reports whether key has BuildKey's shape: what a reader of a
// socket name, a unit or a pidfile accepts.
func ValidBuildKey(key string) bool { return buildKeyRE.MatchString(key) }
