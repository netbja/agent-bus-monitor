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
// change is small; --dry-run prints it, and the single-approval rule
// (deliversingle.go) reads it.

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
	"cmd/agentbus/deliver.go", "cmd/agentbus/deliversmall.go", "cmd/agentbus/deliversingle.go", "filepick/ignore.go", "filepick/glob.go",
	"mergequeue/**",
	// the settings, whole: the keys and their defaults (§6 Q5), their reads
	// (same_provider_ok's project-only read, ProjectValue) and writes (the
	// refusal of an agent's write to a PersonOnly key), the validators and
	// whatever helper the policy calls next (reviewer-codex, foureyes)
	"config/**",
}

// docGlobs is documentation: a change whose every path matches one is
// docs-only (§4.8's docs class). It is small within the same limits as any
// other change (§6 Q2, the person's answer), not at any size.
var docGlobs = []string{"docs/**", "*.md"}

// smallClass is what classify found.
type smallClass struct {
	Small        bool
	Docs         bool // every path is documentation (docGlobs), none never small, binary or unreadable
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
	c.Small = c.Lines <= maxLines && c.Files <= maxFiles
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
	}
	return c.Reason() + ": " + size
}

// Reason is why the change is not small, "small" when it is.
func (c smallClass) Reason() string {
	switch {
	case c.Small:
		return "small"
	case c.Never != "":
		return "not small (" + c.Never + " is never small)"
	case c.Binary != "":
		return "not small (" + c.Binary + " is binary)"
	case c.Unread != "":
		return "not small (" + c.Unread + ")"
	case c.Lines > c.MaxLines:
		return fmt.Sprintf("not small (%d lines > %d)", c.Lines, c.MaxLines)
	}
	return fmt.Sprintf("not small (%d files > %d)", c.Files, c.MaxFiles)
}

// defaultPolicy is §3.1's defaults: what --dry-run measures with when no
// settings were read.
var defaultPolicy = smallPolicy{Mode: "off", MaxLines: 150, MaxFiles: 5, SameProvider: "off"}

// sizeOf classifies H's diff against its merge-base with baseRef under p's
// limits and globs; why says what could not be read.
func sizeOf(git func(args ...string) (string, error), baseRef, h string, p smallPolicy) (c smallClass, why string) {
	mb, err := git("merge-base", baseRef, h)
	mb = strings.TrimSpace(mb)
	if err != nil || mb == "" {
		return c, "no merge-base with " + baseRef
	}
	ns, err := git("diff", "--no-renames", "--numstat", "-z", mb, h)
	if err != nil {
		return c, "the diff against the merge-base could not be read"
	}
	return classify(ns, p.Never, p.MaxLines, p.MaxFiles), ""
}
