package memory

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Question is an open question recorded in the repository
// (docs/memory/open-questions): busmon's Work page lists it under IDEAS
// (requests-plan-redesign, Q2), read-only.
type Question struct {
	ID    string // the note's id, else its file name without .md
	Title string // its first "# " heading, else its id
	Path  string // relative to the repository's root, slash-separated
}

// OpenQuestions are the question notes under root still undecided (status
// proposed or active), in file-name order. No directory: none, no error.
func OpenQuestions(root string) ([]Question, error) {
	rel := "docs/memory/open-questions"
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Question
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel), e.Name()))
		if err != nil {
			return nil, err
		}
		fields, body, ok := splitFrontmatter(string(data))
		if !ok || (fields["status"] != "active" && fields["status"] != "proposed") {
			continue
		}
		q := Question{ID: fields["id"], Path: rel + "/" + e.Name()}
		if q.ID == "" {
			q.ID = strings.TrimSuffix(e.Name(), ".md")
		}
		for _, line := range strings.Split(body, "\n") {
			if t, found := strings.CutPrefix(strings.TrimSpace(line), "# "); found {
				q.Title = strings.TrimSpace(t)
				break
			}
		}
		if q.Title == "" {
			q.Title = q.ID
		}
		out = append(out, q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
