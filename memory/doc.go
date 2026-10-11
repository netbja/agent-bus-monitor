// Package memory checks and maintains the shared memory under docs/memory: the
// rules check-memory.py enforced, the index copy in AGENTS.md, and new notes.
// It reads and writes files in a working tree and nothing else — no Git
// commands, no network, no Redis.
package memory

import (
	"regexp"
	"strings"
)

var (
	// linkRE is a Markdown link; a match preceded by '!' is an image and is
	// skipped by the callers (Go's regexp has no lookbehind).
	linkRE      = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
	codeFileRE  = regexp.MustCompile("`([A-Za-z0-9_.][A-Za-z0-9_./-]*\\.md)`")
	optionalRE  = regexp.MustCompile(`<!-- optional-file: ([^|\n]+)\|\s*(.+?)\s*-->`)
	fieldRE     = regexp.MustCompile(`(?m)^([a-z_]+):\s*(.*?)\s*$`)
	commentRE   = regexp.MustCompile(`(?s)<!--.*?-->`)
	fallbackRE  = regexp.MustCompile(`(?i)\bif (missing|absent|unavailable)\b`)
	schemeRE    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
	noteDirs    = map[string]bool{"decisions": true, "lessons": true, "open-questions": true, "journal": true}
	fieldNames  = []string{"author", "confidence", "created", "id", "kind", "last_verified", "project", "scope", "status"}
	fieldChoice = []struct {
		key     string
		allowed []string
	}{
		{"scope", []string{"project", "transferable"}},
		{"kind", []string{"decision", "lesson", "question", "journal"}},
		{"status", []string{"proposed", "active", "resolved", "superseded"}},
		{"confidence", []string{"verified", "inferred", "unknown"}},
	}
)

// link is one Markdown link (not an image): its raw target and byte offset.
type link struct {
	target string
	pos    int
}

func links(text string) []link {
	var out []link
	for _, m := range linkRE.FindAllStringSubmatchIndex(text, -1) {
		if m[0] > 0 && text[m[0]-1] == '!' {
			continue
		}
		out = append(out, link{text[m[2]:m[3]], m[0]})
	}
	return out
}

// splitFrontmatter follows Python's text.split('---', 2): ok only with three
// parts and nothing but whitespace before the first fence.
func splitFrontmatter(text string) (fields map[string]string, body string, ok bool) {
	parts := strings.SplitN(text, "---", 3)
	if len(parts) != 3 || strings.TrimSpace(parts[0]) != "" {
		return nil, "", false
	}
	fields = map[string]string{}
	for _, m := range fieldRE.FindAllStringSubmatch(parts[1], -1) {
		fields[m[1]] = m[2]
	}
	return fields, parts[2], true
}
