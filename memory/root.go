package memory

import (
	"errors"
	"os"
	"path/filepath"
)

// FindRoot returns the nearest ancestor of dir (dir included) that holds a
// .git entry — a directory, or a file in a worktree.
func FindRoot(dir string) (string, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", errors.New("not inside a git working tree")
		}
		d = parent
	}
}
