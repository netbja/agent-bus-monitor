package memory

import (
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Options configures Check. Today is the date "future" and "stale" are
// measured against (zero = now); StaleAfter is the staleness threshold (zero =
// 60 days).
type Options struct {
	Root       string
	Today      time.Time
	StaleAfter time.Duration
}

// Result is what Check found. Errors fail the check; Optional lists the
// documented optional files that are missing; Warnings never fail it unless
// the caller asks (--strict).
type Result struct {
	Errors, Optional, Warnings []string
}

var requiredEntries = []string{"AGENTS.md", "MEMORY.md", "docs/PROJECT-JOURNAL.md",
	"docs/memory/INDEX.md", "docs/memory/PROTOCOL.md", "docs/memory/journal/INDEX.md"}

// Check runs every rule over the tree at o.Root. The first part is a port of
// scripts/check-memory.py, rule for rule and message for message.
func Check(o Options) Result {
	if o.Today.IsZero() {
		o.Today = time.Now()
	}
	root := resolve(o.Root)
	c := &checker{root: root, memory: filepath.Join(root, "docs", "memory"), today: o.Today.Format("2006-01-02"), edges: map[string][]string{}}
	c.run()
	return c.res
}

type checker struct {
	root, memory, today string
	edges               map[string][]string
	res                 Result
}

func (c *checker) fail(s string) { c.res.Errors = append(c.res.Errors, s) }

func (c *checker) rel(p string) string {
	r, err := filepath.Rel(c.root, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(r)
}

func (c *checker) run() {
	bootSet := map[string]bool{filepath.Join(c.root, "CLAUDE.md"): true}
	for _, n := range []string{"agent-bus", "agent-bus-master", "agent-bus-sentinel"} {
		bootSet[filepath.Join(c.root, "skills", n, "SKILL.md")] = true
	}
	for _, n := range []string{"architect", "coder", "deploy", "foureyes", "master", "sentinel"} {
		bootSet[filepath.Join(c.root, "roles", n+".md")] = true
	}
	for _, g := range []string{"skills/*/SKILL.md", "roles/*.md"} {
		m, _ := filepath.Glob(filepath.Join(c.root, filepath.FromSlash(g)))
		for _, p := range m {
			bootSet[p] = true
		}
	}
	boot := sortedPaths(bootSet)

	docSet := map[string]bool{}
	for _, p := range c.memoryNotes() {
		docSet[p] = true
	}
	for _, p := range boot {
		docSet[p] = true
	}
	for _, p := range requiredEntries {
		docSet[filepath.Join(c.root, filepath.FromSlash(p))] = true
	}

	for _, name := range requiredEntries {
		if !isFile(filepath.Join(c.root, filepath.FromSlash(name))) {
			c.fail("missing required entry point: " + name)
		}
	}
	for _, src := range boot {
		b, err := os.ReadFile(src)
		if err != nil || !strings.Contains(string(b), "docs/memory/INDEX.md") {
			c.fail("boot path does not reference memory index: " + c.rel(src))
		}
	}
	for _, src := range sortedPaths(docSet) {
		if isFile(src) {
			c.document(src)
		}
	}
	c.reachability()
	c.blockRule()
}

// memoryNotes lists every .md under docs/memory (Python's rglob).
func (c *checker) memoryNotes() []string {
	var out []string
	filepath.WalkDir(c.memory, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func (c *checker) document(src string) {
	b, _ := os.ReadFile(src)
	text := string(b)
	label := c.rel(src)
	isSkill := filepath.Base(src) == "SKILL.md"
	optional := map[string]string{}
	for _, m := range optionalRE.FindAllStringSubmatch(text, -1) {
		optional[strings.TrimSpace(m[1])] = strings.TrimSpace(m[2])
	}
	key := resolve(src)
	c.edges[key] = nil

	// fallbackAllowed: internal reference memory must never be made optional;
	// exceptions are for concrete, project-specific resources named by a skill.
	fallbackAllowed := func(target string, pos int) bool {
		why, ok := optional[target]
		if !isSkill || !ok {
			return false
		}
		for _, p := range []string{resolve(join(filepath.Dir(src), target)), resolve(join(c.root, target))} {
			if within(p, c.memory) || p == filepath.Join(c.root, "MEMORY.md") || p == filepath.Join(c.root, "docs", "PROJECT-JOURNAL.md") {
				return false
			}
		}
		visible := commentRE.ReplaceAllString(runeWindow(text, pos, 400, 600), "")
		if !fallbackRE.MatchString(why) || len(strings.Fields(why)) < 5 || !strings.Contains(visible, why) {
			c.fail(label + ": optional-file " + target + " needs a visible nearby fallback instruction")
			return false
		}
		w := label + ": optional missing file " + target + "; fallback: " + why
		if !slices.Contains(c.res.Optional, w) {
			c.res.Optional = append(c.res.Optional, w)
		}
		return true
	}

	for _, l := range links(text) {
		// Standard Markdown paths only; external URLs and anchors are not fetched.
		target := strings.Trim(strings.TrimSpace(l.target), "<>")
		pth, ok := localPath(target)
		if !ok {
			continue
		}
		p := resolve(join(filepath.Dir(src), pth))
		switch {
		case !within(p, c.root):
			c.fail(label + ": local link escapes repository: " + target)
		case !exists(p) && !fallbackAllowed(target, l.pos):
			c.fail(label + ": broken local link: " + target)
		case isFile(p):
			c.edges[key] = append(c.edges[key], p)
		}
	}

	if isSkill {
		for _, m := range codeFileRE.FindAllStringSubmatchIndex(text, -1) {
			target := text[m[2]:m[3]]
			// Bare protocol names next to links are relative to the skill;
			// examples with repo paths are resolved from the repository root.
			if isFile(join(c.root, target)) || isFile(join(filepath.Dir(src), target)) {
				continue
			}
			linked := false
			for _, l := range linkRE.FindAllStringSubmatch(text, -1) {
				lp := splitPath(l[1])
				if filepath.Base(lp) == target && isFile(join(filepath.Dir(src), lp)) {
					linked = true
				}
			}
			if !linked && !fallbackAllowed(target, m[0]) {
				c.fail(label + ": missing skill file reference: " + target + "; provide optional-file fallback")
			}
		}
	}

	if noteDirs[filepath.Base(filepath.Dir(src))] && filepath.Base(src) != "INDEX.md" {
		c.frontmatter(label, text)
	}
}

func (c *checker) frontmatter(label, text string) {
	fields, body, ok := splitFrontmatter(text)
	if !ok {
		c.fail(label + ": missing YAML frontmatter")
		return
	}
	for _, k := range fieldNames {
		if fields[k] == "" {
			c.fail(label + ": missing metadata " + k)
		}
	}
	for _, ch := range fieldChoice {
		if !slices.Contains(ch.allowed, fields[ch.key]) {
			c.fail(label + ": invalid " + ch.key)
		}
	}
	for _, k := range []string{"created", "last_verified"} {
		v := fields[k]
		if k == "last_verified" && v == "unknown" && fields["confidence"] != "verified" {
			continue
		}
		if !validDate(v) {
			c.fail(label + ": invalid " + k + " date")
			continue
		}
		if v > c.today {
			c.fail(label + ": " + k + " is in the future")
		}
		if k == "last_verified" {
			// Python compared against date.fromisoformat(created), which raised
			// (and reported this same message) when created did not parse.
			if !validDate(fields["created"]) {
				c.fail(label + ": invalid " + k + " date")
			} else if v < fields["created"] {
				c.fail(label + ": last_verified predates created")
			}
		}
	}
	if len(links(body)) == 0 {
		c.fail(label + ": no linked evidence/context")
	}
}

func (c *checker) reachability() {
	reached := map[string]bool{}
	todo := []string{resolve(filepath.Join(c.memory, "INDEX.md"))}
	for len(todo) > 0 {
		p := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if !reached[p] {
			reached[p] = true
			todo = append(todo, c.edges[p]...)
		}
	}
	for _, p := range c.memoryNotes() {
		if !reached[resolve(p)] {
			c.fail("memory note unreachable from INDEX: " + c.rel(p))
		}
	}
}

// localPath returns the path part of a link target that names a local file, as
// Python's urlsplit did: no scheme, no netloc, a non-empty path; unquoted.
func localPath(target string) (string, bool) {
	if schemeRE.MatchString(target) || strings.HasPrefix(target, "//") {
		return "", false
	}
	p := splitPath(target)
	if p == "" {
		return "", false
	}
	if u, err := url.PathUnescape(p); err == nil {
		p = u
	}
	return p, true
}

// splitPath drops a ?query and a #fragment.
func splitPath(target string) string {
	if i := strings.IndexAny(target, "?#"); i >= 0 {
		return target[:i]
	}
	return target
}

// join is pathlib's "/": an absolute right side replaces the left.
func join(dir, p string) string {
	p = filepath.FromSlash(p)
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(dir, p)
}

// resolve is pathlib's non-strict resolve: absolute, cleaned, symlinks
// followed where the path exists.
func resolve(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		p = a
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func within(p, dir string) bool {
	r, err := filepath.Rel(dir, p)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// runeWindow is Python's text[max(0, pos-before):pos+after] with pos a byte
// offset converted to a rune index.
func runeWindow(text string, pos, before, after int) string {
	r := []rune(text)
	i := len([]rune(text[:pos]))
	return string(r[max(0, i-before):min(len(r), i+after)])
}

// sortedPaths orders paths component by component, as Python sorts Paths.
func sortedPaths(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b string) int {
		return slices.Compare(strings.Split(a, string(filepath.Separator)), strings.Split(b, string(filepath.Separator)))
	})
	return out
}
