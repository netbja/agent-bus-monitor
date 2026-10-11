package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// FillMarker marks a section of a note that mem new left for its author;
// a note that still carries it fails the check.
const FillMarker = "<!-- mem:fill -->"

const (
	indexMaxLines   = 60
	indexMaxBytes   = 4096
	defaultStaleAge = 60 * 24 * time.Hour
)

var (
	labelledLinkRE = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)
	statusLabelRE  = regexp.MustCompile(`\((proposed|active|resolved|superseded)\)\s*$`)
)

// newRules are the checks check-memory.py did not have (spec §3.1).
func (c *checker) newRules() {
	index, err := os.ReadFile(filepath.Join(c.memory, "INDEX.md"))
	if err == nil {
		c.labelRule(string(index))
		c.budgetRule(index)
	}
	for _, p := range c.memoryNotes() {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if !noteDirs[filepath.Base(filepath.Dir(p))] || filepath.Base(p) == "INDEX.md" {
			continue // the protocol may quote the marker; only notes are checked
		}
		if strings.Contains(string(b), FillMarker) {
			c.fail(c.rel(p) + ": template not filled (" + FillMarker + " remains)")
		}
		c.staleRule(p, string(b))
	}
}

// labelRule: an INDEX link labelled with a status must agree with the note.
func (c *checker) labelRule(index string) {
	for _, m := range labelledLinkRE.FindAllStringSubmatchIndex(index, -1) {
		if m[0] > 0 && index[m[0]-1] == '!' {
			continue
		}
		lm := statusLabelRE.FindStringSubmatch(index[m[2]:m[3]])
		pth, ok := localPath(strings.Trim(strings.TrimSpace(index[m[4]:m[5]]), "<>"))
		if lm == nil || !ok {
			continue
		}
		target := join(c.memory, pth)
		b, err := os.ReadFile(target)
		if err != nil {
			continue // a broken link is reported by the link rule
		}
		if fields, _, ok := splitFrontmatter(string(b)); ok && fields["status"] != lm[1] {
			c.fail(fmt.Sprintf("docs/memory/INDEX.md: link labelled (%s) but %s has status %s", lm[1], c.rel(target), fields["status"]))
		}
	}
}

// budgetRule: the index is loaded at every boot, so it stays small.
func (c *checker) budgetRule(index []byte) {
	lines := strings.Count(string(index), "\n")
	if len(index) > 0 && index[len(index)-1] != '\n' {
		lines++
	}
	if lines > indexMaxLines || len(index) > indexMaxBytes {
		c.fail(fmt.Sprintf("docs/memory/INDEX.md: %d lines, %d bytes; the index is loaded at every boot and must stay within %d lines and %d bytes",
			lines, len(index), indexMaxLines, indexMaxBytes))
	}
}

// staleRule warns, never fails: an active verified note not re-checked for
// staleAfter, or a proposal left undecided as long. Journal checkpoints are
// dated records and resolved/superseded notes are no longer relied on.
func (c *checker) staleRule(p, text string) {
	f, _, ok := splitFrontmatter(text)
	if !ok || f["kind"] == "journal" {
		return
	}
	cut := c.now.Add(-c.staleAfter).Format("2006-01-02")
	days := int(c.staleAfter / (24 * time.Hour))
	switch {
	case f["status"] == "active" && f["confidence"] == "verified" && validDate(f["last_verified"]) && f["last_verified"] < cut:
		c.warn(fmt.Sprintf("%s: verified on %s, older than %dd; re-check its evidence", c.rel(p), f["last_verified"], days))
	case f["status"] == "proposed" && validDate(f["created"]) && f["created"] < cut:
		c.warn(fmt.Sprintf("%s: proposed since %s, older than %dd; decide or supersede it", c.rel(p), f["created"], days))
	}
}

func (c *checker) warn(s string) { c.res.Warnings = append(c.res.Warnings, s) }
