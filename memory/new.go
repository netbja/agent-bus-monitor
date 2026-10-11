package memory

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/netbja/agent-bus-monitor/bus"
)

// NewOptions describes the note mem new creates. Scope "" means project;
// NoteProject is needed only when the existing notes do not agree on one.
type NewOptions struct {
	Root, Kind, Slug, Title, Author, Scope, NoteProject string
	Today                                               time.Time
}

var (
	slugRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
	newKinds = map[string]struct{ dir, heading string }{
		"lesson":   {"lessons", "## Lessons"},
		"decision": {"decisions", "## Decisions"},
		"question": {"open-questions", "## Open questions"},
	}
)

const noteBody = `# %s

## Finding/decision

` + FillMarker + ` What was found or decided, in two or three sentences.

## When it applies

` + FillMarker + ` The situation in which the next agent needs this.

## Evidence

` + FillMarker + ` Link at least one committed file, PR or test as a Markdown link.

## Limits / what would invalidate it

` + FillMarker + ` What this does not prove, and what would make it false.
`

// NewNote creates a dated note whose frontmatter holds only facts (status
// proposed, nothing verified), appends its line to the matching INDEX
// section and re-syncs AGENTS.md. Every check runs before the first write.
// It returns the note's slash path relative to the root.
func NewNote(o NewOptions) (string, error) {
	if o.Today.IsZero() {
		o.Today = time.Now()
	}
	memDir := filepath.Join(o.Root, "docs", "memory")
	indexPath := filepath.Join(memDir, "INDEX.md")
	index, err := os.ReadFile(indexPath)
	if err != nil {
		return "", errors.New("no docs/memory/INDEX.md under " + o.Root)
	}
	k, ok := newKinds[o.Kind]
	switch {
	case !ok:
		return "", fmt.Errorf("unknown kind %q: lesson, decision or question", o.Kind)
	case !slugRE.MatchString(o.Slug):
		return "", fmt.Errorf("invalid slug %q: lowercase letters, digits and dashes, at most 48", o.Slug)
	case strings.TrimSpace(o.Title) == "" || strings.ContainsAny(o.Title, "[]") || strings.ContainsFunc(o.Title, unicode.IsControl):
		return "", fmt.Errorf("invalid title %q: one line, not empty, no [ or ]", o.Title)
	case o.Author == "":
		return "", errors.New("AGENT_BUS_AGENT is not set: the note needs an author")
	case !bus.ValidName(o.Author):
		return "", fmt.Errorf("invalid author %q", o.Author)
	case o.Scope != "" && o.Scope != "project" && o.Scope != "transferable":
		return "", fmt.Errorf("invalid scope %q: project or transferable", o.Scope)
	case o.NoteProject != "" && !bus.ValidName(o.NoteProject):
		return "", fmt.Errorf("invalid project %q", o.NoteProject)
	}
	project := o.NoteProject
	if project == "" {
		if project, err = sharedProject(memDir); err != nil {
			return "", err
		}
	}
	date := o.Today.Format("2006-01-02")
	stem := date + "-" + o.Slug
	rel := k.dir + "/" + stem + ".md"
	notePath := filepath.Join(memDir, k.dir, stem+".md")
	if _, err := os.Lstat(notePath); err == nil {
		return "", errors.New("docs/memory/" + rel + " already exists")
	}
	if !within(resolve(notePath), resolve(o.Root)) {
		return "", errors.New("docs/memory/" + rel + " resolves outside the repository")
	}
	nextIndex, err := appendToSection(string(index), k.heading, "- ["+strings.TrimSpace(o.Title)+"]("+rel+")")
	if err != nil {
		return "", err
	}
	if n, b := indexSize([]byte(nextIndex)); n > indexMaxLines || b > indexMaxBytes {
		return "", fmt.Errorf("docs/memory/INDEX.md would reach %d lines, %d bytes; the index must stay within %d lines and %d bytes: trim it first", n, b, indexMaxLines, indexMaxBytes)
	}
	if agents, err := os.ReadFile(filepath.Join(o.Root, "AGENTS.md")); err != nil {
		return "", err
	} else if _, _, ok := blockSpan(string(agents)); !ok {
		return "", errors.New("AGENTS.md has no " + BeginMarker + " block to sync")
	}
	scope := o.Scope
	if scope == "" {
		scope = "project"
	}

	front := fmt.Sprintf("---\nid: %s\nproject: %s\nscope: %s\nkind: %s\nstatus: proposed\ncreated: %s\nlast_verified: unknown\nauthor: %s\nconfidence: unknown\n---\n",
		stem, project, scope, o.Kind, date, o.Author)
	if err := os.MkdirAll(filepath.Dir(notePath), 0o755); err != nil {
		return "", err
	}
	// O_EXCL claims the name atomically and never follows a link planted there.
	f, err := os.OpenFile(notePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	_, werr := f.WriteString(front + fmt.Sprintf(noteBody, strings.TrimSpace(o.Title)))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.WriteFile(indexPath, []byte(nextIndex), 0o644)
	}
	if werr != nil {
		os.Remove(notePath) // no orphan note without its index line
		return "", werr
	}
	if _, err := Sync(o.Root); err != nil {
		return "", err
	}
	return "docs/memory/" + rel, nil
}

// sharedProject is the project every existing note names.
func sharedProject(memDir string) (string, error) {
	var seen []string
	filepath.WalkDir(memDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") || !noteDirs[filepath.Base(filepath.Dir(p))] {
			return nil
		}
		b, _ := os.ReadFile(p)
		if f, _, ok := splitFrontmatter(string(b)); ok && f["project"] != "" && !slices.Contains(seen, f["project"]) {
			seen = append(seen, f["project"])
		}
		return nil
	})
	switch len(seen) {
	case 0:
		return "", errors.New("no existing note names a project: pass --note-project")
	case 1:
		return seen[0], nil
	}
	slices.Sort(seen)
	return "", fmt.Errorf("notes disagree on project (%s): pass --note-project", strings.Join(seen, ", "))
}

// appendToSection puts line after the last list item of the section whose
// heading starts with heading, or right under the heading when it has none.
func appendToSection(index, heading, line string) (string, error) {
	lines := strings.Split(index, "\n")
	h := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, heading) })
	if h < 0 {
		return "", errors.New("docs/memory/INDEX.md has no " + heading + " section")
	}
	end := len(lines)
	for i := h + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "#") {
			end = i
			break
		}
	}
	last := -1
	for i := h + 1; i < end; i++ {
		if strings.HasPrefix(lines[i], "- ") {
			last = i
		}
	}
	if last >= 0 {
		return strings.Join(slices.Insert(lines, last+1, line), "\n"), nil
	}
	// An empty section: heading, blank line, the item, then a blank line
	// before whatever follows (or the final newline).
	j := h + 1
	for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
		j++
	}
	insert := []string{"", line, ""}
	return strings.Join(append(append(lines[:h+1:h+1], insert...), lines[j:]...), "\n"), nil
}
