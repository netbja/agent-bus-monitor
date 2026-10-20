package main

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// openPTY returns a pseudo-terminal pair from /dev/ptmx.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	if err := unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("unlockpt: %v", err)
	}
	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skipf("ptsname: %v", err)
	}
	s, err := os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open slave: %v", err)
	}
	return m, s
}

// A real bash on a real terminal: at its prompt the check says idle; while
// a builtin read waits, and while a command runs, it says busy.
func TestShellAtPromptTellsAPromptFromABuiltinRead(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	m, s := openPTY(t)
	defer m.Close()
	cmd := exec.Command(bash, "--norc", "--noprofile", "-i")
	cmd.Env = []string{"TERM=xterm", "PS1=$ ", "HOME=" + t.TempDir()}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = s, s, s
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	go func() { // drain the output so bash never blocks writing
		buf := make([]byte, 4096)
		for {
			if _, err := m.Read(buf); err != nil {
				return
			}
		}
	}()
	pid := cmd.Process.Pid
	await := func(want bool, what string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			got, err := shellAtPrompt(pid, "bash")
			if err == nil && got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: at prompt = %v (%v), want %v", what, got, err, want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	await(true, "at the prompt")
	if _, err := m.WriteString("read -r answer\n"); err != nil {
		t.Fatal(err)
	}
	await(false, "in a builtin read")
	if _, err := m.WriteString("x\n"); err != nil {
		t.Fatal(err)
	}
	await(true, "back at the prompt")
	if _, err := m.WriteString("sleep 5\n"); err != nil {
		t.Fatal(err)
	}
	await(false, "running a command")
	if ok, _ := shellAtPrompt(pid, "sh"); ok {
		t.Fatal("a shell without a line editor was judged at its prompt")
	}
}
