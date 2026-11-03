package notify

import (
	"regexp"
	"strings"

	"github.com/netbja/agent-bus-monitor/health"
)

// Scrub is the full detail's best effort over an agent's free text (spec
// "The message"): health.Redact first (the whole field on any
// credential-like hit), then every URL ([url]), every absolute or ~/ path
// holding a second slash ([path]) and every email address ([email]). summary,
// the default, sends no free text at all.
func Scrub(s string) string {
	s = health.Redact(s)
	if s == health.Redacted {
		return s
	}
	s = urlRE.ReplaceAllString(s, "[url]")
	s = emailRE.ReplaceAllString(s, "[email]")
	return pathRE.ReplaceAllString(s, "${1}[path]")
}

var (
	urlRE   = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://\S+|\bwww\.\S+`)
	emailRE = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+`)
	// A word starting with / or ~/ and holding a second slash.
	pathRE = regexp.MustCompile(`(^|\s)(~/|/)[^\s/]*/\S*`)
)

// detailMax is how many runes of an agent's text a full line carries.
const detailMax = 120

// DetailText is s scrubbed, on one line, cut to 120 runes.
func DetailText(s string) string {
	s = strings.Join(strings.Fields(Scrub(s)), " ")
	r := []rune(s)
	if len(r) > detailMax {
		return string(r[:detailMax-1]) + "…"
	}
	return s
}
