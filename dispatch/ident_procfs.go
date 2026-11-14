//go:build !darwin

package dispatch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// procStat reads /proc/<pid>/stat (Linux). A pid /proc lists no more, while
// /proc itself is readable, is ErrNoProcess; without /proc nothing is known.
func procStat(pid int) (statFields, error) {
	f, err := readStat(pid)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if _, serr := os.Stat("/proc/self/stat"); serr == nil {
				return statFields{}, fmt.Errorf("pid %d: %w", pid, ErrNoProcess)
			}
		}
		return statFields{}, err
	}
	return f, nil
}

func bootID() (string, error) {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return string(b), err
}
