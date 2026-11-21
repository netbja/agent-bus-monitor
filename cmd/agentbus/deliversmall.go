package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/netbja/agent-bus-monitor/filepick"
)

// The small-change classifier (spec 2026-11-21-single-approval-design §3.2,
// build step 1): deliver decides from the merge-base diff it is about to
// deliver, `git diff --no-renames --numstat -z <merge-base> <H>`, whether a
// change is small. Step 1 only prints it (--dry-run); no rule reads it yet.

// neverSmall is §3.2's default list: a path matching one makes the change
// not small whatever its size. A setting may add globs, never remove one.
// The gate's own files are on it, this file and its glob matcher included,
// so a small PR cannot loosen the classifier and then pass on one approval.
var neverSmall = []string{
	// protocol
	"bus/**",
	// broker, ACL
	"docker-compose.yml", "internal/brokerrt/**", "**/*.acl", "setup/redis.go", "setup/rotate.go",
	// setup, update, rollback
	"setup.sh", "setup/**", "scripts/bootstrap", "update/**", "cmd/agentbus/updatecmd.go", "backup/**",
	// secrets
	"health/health.go", "internal/secretpath/**", "trust/**",
	// CI
	".github/workflows/**", ".gitlab-ci.yml", "bitbucket-pipelines.yml",
	// skills (a path cannot tell a safety rule from other skill text)
	"skills/**",
	// the gate itself
	"cmd/agentbus/deliver.go", "cmd/agentbus/deliversmall.go", "filepick/ignore.go", "filepick/glob.go",
	"mergequeue/**", "config/keys.go",
}

// docGlobs is documentation: a change whose every path matches one is small
// at any size (§3.2, the person's "spec/docs-only").
var docGlobs = []string{"docs/**", "*.md"}

// smallClass is what classify found.
type smallClass struct {
	Small, Docs  bool
	Lines, Files int
	Never        string // the first path a never-small glob matched
	Binary       string // the first binary path
	Unread       string // why a row could not be read
	MaxLines     int
	MaxFiles     int
}

// classify reads numstat: git diff --no-renames --numstat, -z (NUL after
// each row, paths unquoted) or not (a row per line). A rename is a deleted
// and an added path, two files. extra adds never-small globs (blank ones
// are skipped); maxLines and maxFiles are the limits. A binary row, a row
// it cannot read, or a path git quoted (non -z: it would hide the real name
// from the globs) makes the change not small.
func classify(numstat string, extra []string, maxLines, maxFiles int) smallClass {
	c := smallClass{MaxLines: maxLines, MaxFiles: maxFiles, Docs: true}
	globs := append(append([]string(nil), neverSmall...), extra...)
	sep := "\n"
	if strings.Contains(numstat, "\x00") {
		sep = "\x00"
	}
	unread := func(why string) {
		if c.Unread == "" {
			c.Unread = why
		}
	}
	for _, row := range strings.Split(numstat, sep) {
		if strings.TrimSpace(row) == "" {
			continue
		}
		c.Files++
		f := strings.SplitN(row, "\t", 3)
		if len(f) != 3 || f[2] == "" {
			unread("a row of the diff cannot be read")
			continue
		}
		p := f[2]
		if sep == "\n" && strings.HasPrefix(p, `"`) {
			unread("a path git quoted cannot be read")
		}
		add, aerr := strconv.Atoi(f[0])
		del, derr := strconv.Atoi(f[1])
		switch {
		case f[0] == "-" && f[1] == "-":
			if c.Binary == "" {
				c.Binary = p
			}
		case aerr != nil || derr != nil || add < 0 || del < 0:
			unread("a row of the diff cannot be read")
		default:
			c.Lines += add + del
		}
		if c.Never == "" && matchesAny(globs, p) {
			c.Never = p
		}
		if !matchesAny(docGlobs, p) {
			c.Docs = false
		}
	}
	if c.Never != "" || c.Binary != "" || c.Unread != "" {
		c.Docs = false
		return c
	}
	c.Small = c.Docs || c.Lines <= maxLines && c.Files <= maxFiles
	return c
}

func matchesAny(globs []string, p string) bool {
	for _, g := range globs {
		if filepick.MatchGlob(g, p) {
			return true
		}
	}
	return false
}

// String is the line --dry-run prints.
func (c smallClass) String() string {
	size := fmt.Sprintf("%d lines, %d files", c.Lines, c.Files)
	switch {
	case c.Small && c.Docs:
		return "small (docs only): " + size
	case c.Small:
		return "small: " + size
	case c.Never != "":
		return "not small (" + c.Never + " is never small): " + size
	case c.Binary != "":
		return "not small (" + c.Binary + " is binary): " + size
	case c.Unread != "":
		return "not small (" + c.Unread + "): " + size
	case c.Lines > c.MaxLines:
		return fmt.Sprintf("not small (%d lines > %d): %s", c.Lines, c.MaxLines, size)
	}
	return fmt.Sprintf("not small (%d files > %d): %s", c.Files, c.MaxFiles, size)
}

// Step 1's limits: §3.1's defaults, until step 2 reads them from the
// project's settings.
const smallMaxLines, smallMaxFiles = 150, 5

// sizeClass classifies H's diff against its merge-base with baseRef, or
// says it could not be read.
func sizeClass(git func(args ...string) (string, error), baseRef, h string) string {
	mb, err := git("merge-base", baseRef, h)
	mb = strings.TrimSpace(mb)
	if err != nil || mb == "" {
		return "unknown (no merge-base with " + baseRef + ")"
	}
	ns, err := git("diff", "--no-renames", "--numstat", "-z", mb, h)
	if err != nil {
		return "unknown (the diff against the merge-base could not be read)"
	}
	return classify(ns, nil, smallMaxLines, smallMaxFiles).String()
}
