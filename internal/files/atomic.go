// Package files holds the one atomic file writer of the module's state files
// (architect's cmd extraction map, step 0a): lifecycle's counters, quota's
// readings, config.toml and busmon's local cache all write through it.
// setup's writer stays apart: it replaces a file through its symlink and
// keeps the file's mode, another operation.
package files

import (
	"os"
	"path/filepath"
)

// The steps a test replaces to observe or fail them.
var (
	writeFile = func(f *os.File, b []byte) (int, error) { return f.Write(b) }
	syncFile  = func(f *os.File) error { return f.Sync() }
	syncDir   = func(dir string) error {
		d, err := os.Open(dir)
		if err != nil {
			return err
		}
		defer d.Close()
		return d.Sync()
	}
)

// WriteAtomic writes b to path so that a crash leaves the old file or the new
// one, never a part:
//   - the directory is made 0700 when missing (an existing one keeps its mode);
//   - b goes to a 0600 temporary file beside path, fsync'd, then renamed over
//     path (a symlink at path is replaced, not followed);
//   - the directory is fsync'd, so the new name survives a crash too.
//
// Any failure before the rename leaves the old file whole and no temporary
// file behind.
func WriteAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // a no-op once renamed
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := writeFile(f, b); err != nil {
		f.Close()
		return err
	}
	if err := syncFile(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(dir)
}
