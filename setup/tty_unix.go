//go:build linux || darwin

package setup

import (
	"os"

	"golang.org/x/sys/unix"
)

// IsTerminal is a real isatty (the terminal attributes ioctl), never a
// character-device check: /dev/null is one.
func IsTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), ioctlGetTermios)
	return err == nil
}
