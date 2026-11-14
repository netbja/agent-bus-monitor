package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The operator notes are fragments (operator-notes-fragments, the person's
// hot-files rule): docs/operator-notes.d/ holds _header.md, then one bullet
// per file. The migrated bullets are numbered (0010-<slug>.md) and keep the
// order they had; each change adds its own fragment, named by its task
// (<task>.md), which sorts after them. No PR edits docs/operator-notes.md:
// `agentbus mem notes` assembles it at a release.

// NotesDir and NotesFile are the fragments and the assembled notes, under a
// repository root.
const (
	NotesDir  = "docs/operator-notes.d"
	NotesFile = "docs/operator-notes.md"
	// notesStamp is the hash of the file mem notes last wrote: a file that
	// no longer has it was edited by hand (foureyes).
	notesStamp = "_assembled.sha256"
)

// fragmentName is a fragment's name: a migrated one's number and slug, or a
// task's name.
var fragmentName = regexp.MustCompile(`^([0-9]{4}-)?[a-z][a-z0-9_-]*\.md$`)

// AssembleNotes reads root's fragments and returns the notes: the header,
// then the fragments by name. A fragment that is not one bullet ending in a
// newline, or a file named otherwise, is refused: nothing is assembled from
// a malformed directory.
func AssembleNotes(root string) (string, error) {
	dir := filepath.Join(root, NotesDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	header, err := os.ReadFile(filepath.Join(dir, "_header.md"))
	if err != nil {
		return "", fmt.Errorf("%s: no _header.md", NotesDir)
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "_") { // the header (read above) and the stamp
			continue
		}
		if e.IsDir() || !fragmentName.MatchString(e.Name()) {
			return "", fmt.Errorf("%s/%s: a fragment is <task>.md (lower case, digits, - or _)", NotesDir, e.Name())
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var b strings.Builder
	b.Write(header)
	for _, n := range names {
		body, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return "", err
		}
		if err := checkFragment(string(body)); err != nil {
			return "", fmt.Errorf("%s/%s: %w", NotesDir, n, err)
		}
		b.Write(body)
	}
	return b.String(), nil
}

// checkFragment: one top-level bullet, its continuation lines indented, the
// last line ended.
func checkFragment(s string) error {
	if !strings.HasPrefix(s, "- ") {
		return errors.New("a fragment is one bullet: it starts with \"- \"")
	}
	if !strings.HasSuffix(s, "\n") {
		return errors.New("a fragment ends with a newline")
	}
	for _, line := range strings.Split(strings.TrimSuffix(s, "\n"), "\n")[1:] {
		if line != "" && !strings.HasPrefix(line, " ") {
			return fmt.Errorf("one bullet per fragment: %.40q is not indented under it", line)
		}
	}
	return nil
}

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// NotesCurrent is nil when root's NotesFile is what mem notes last wrote
// (its hash is the stamp's), or does not exist yet; else the file was edited
// by hand, which no PR does: the edit belongs in a fragment.
func NotesCurrent(root string) error {
	b, err := os.ReadFile(filepath.Join(root, NotesFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	stamp, err := os.ReadFile(filepath.Join(root, NotesDir, notesStamp))
	if err != nil || strings.TrimSpace(string(stamp)) != digest(b) {
		return fmt.Errorf("%s was edited by hand since the last agentbus mem notes: move the edit into its fragment in %s (git diff %s), put the file back, then run it again", NotesFile, NotesDir, NotesFile)
	}
	return nil
}

// WriteNotes writes the assembled notes to root's NotesFile and its stamp;
// it writes nothing when they are already current, and refuses to overwrite
// a file edited by hand (NotesCurrent): that edit would be lost.
func WriteNotes(root string) (changed bool, err error) {
	s, err := AssembleNotes(root)
	if err != nil {
		return false, err
	}
	path := filepath.Join(root, NotesFile)
	old, err := os.ReadFile(path)
	switch {
	case err == nil && string(old) == s:
		return false, writeStamp(root, s)
	case err == nil:
		if err := NotesCurrent(root); err != nil {
			return false, err
		}
	case !errors.Is(err, os.ErrNotExist):
		return false, err
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		return false, err
	}
	return true, writeStamp(root, s)
}

func writeStamp(root, s string) error {
	return os.WriteFile(filepath.Join(root, NotesDir, notesStamp), []byte(digest([]byte(s))+"\n"), 0o644)
}
