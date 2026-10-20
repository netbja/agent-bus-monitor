package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// shellAtPrompt is the kernel's word that the shell pid waits at its line
// editor's prompt: its terminal is in non-canonical mode, which bash's
// readline and zsh's ZLE set while they read a command line and undo before
// running anything. A builtin read, a command, or a shell without a line
// editor leaves the terminal canonical, and so reads as busy. Only bash and
// zsh are judged (fish's read uses its own editor, sh has none).
func shellAtPrompt(pid int, argv0 string) (bool, error) {
	switch shellName(argv0) {
	case "bash", "zsh":
	default:
		return false, nil
	}
	tty, err := os.Readlink("/proc/" + itoa(pid) + "/fd/0")
	if err != nil {
		return false, err
	}
	f, err := os.OpenFile(tty, os.O_RDONLY|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()
	t, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	if err != nil {
		return false, err
	}
	return t.Lflag&unix.ICANON == 0, nil
}
