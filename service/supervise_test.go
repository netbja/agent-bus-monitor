package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// script is a program that counts its runs in a file and exits with the code
// the runs list gives (the last code repeats).
func script(t *testing.T, codes ...int) (func() *exec.Cmd, func() int) {
	t.Helper()
	dir := t.TempDir()
	count := filepath.Join(dir, "runs")
	var b strings.Builder
	b.WriteString("#!/bin/sh\nn=$(cat \"$1\" 2>/dev/null || echo 0)\nn=$((n+1))\necho $n > \"$1\"\ncase $n in\n")
	for i, c := range codes[:len(codes)-1] {
		b.WriteString(strconv.Itoa(i+1) + ") exit " + strconv.Itoa(c) + ";;\n")
	}
	b.WriteString("esac\nexit " + strconv.Itoa(codes[len(codes)-1]) + "\n")
	prog := filepath.Join(dir, "prog")
	if err := os.WriteFile(prog, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	runs := func() int {
		b, _ := os.ReadFile(count)
		n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		return n
	}
	return func() *exec.Cmd { return exec.Command(prog, count) }, runs
}

// fakeClock: Sleep advances Now by step (or the asked delay when step is 0).
type fakeClock struct {
	now    time.Time
	step   time.Duration
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) bool {
	c.sleeps = append(c.sleeps, d)
	if c.step > 0 {
		c.now = c.now.Add(c.step)
	} else {
		c.now = c.now.Add(d)
	}
	return ctx.Err() == nil
}

// Spec 4.4b: a non-zero exit is restarted after 2 s, at most 5 starts in
// 300 s, then the supervisor gives up.
func TestSupervisorGivesUpAfterTheBurst(t *testing.T) {
	cmd, runs := script(t, 1)
	c := &fakeClock{now: time.Unix(1000, 0)}
	exits := []int{}
	gaveUp := Supervisor{Command: cmd, Now: c.Now, Sleep: c.Sleep, AfterExit: func(code int) { exits = append(exits, code) }}.Run(context.Background())
	if !gaveUp || runs() != 5 {
		t.Fatalf("gave up %v after %d runs", gaveUp, runs())
	}
	if len(c.sleeps) != 4 || c.sleeps[0] != 2*time.Second {
		t.Fatalf("sleeps %v", c.sleeps)
	}
	if len(exits) != 5 || exits[0] != 1 {
		t.Fatalf("AfterExit saw %v", exits)
	}
}

// An exit 0 ends the supervisor: it is final, never restarted.
func TestSupervisorEndsOnExitZero(t *testing.T) {
	cmd, runs := script(t, 1, 1, 0)
	c := &fakeClock{now: time.Unix(1000, 0)}
	if gaveUp := (Supervisor{Command: cmd, Now: c.Now, Sleep: c.Sleep}).Run(context.Background()); gaveUp || runs() != 3 {
		t.Fatalf("gave up %v after %d runs", gaveUp, runs())
	}
}

// Starts older than the window no longer count: failures spaced out more
// than 300 s per 5 starts are restarted for ever (here until an exit 0).
func TestSupervisorWindowSlides(t *testing.T) {
	cmd, runs := script(t, 1, 1, 1, 1, 1, 1, 1, 0)
	c := &fakeClock{now: time.Unix(1000, 0), step: 100 * time.Second}
	if gaveUp := (Supervisor{Command: cmd, Now: c.Now, Sleep: c.Sleep}).Run(context.Background()); gaveUp || runs() != 8 {
		t.Fatalf("gave up %v after %d runs", gaveUp, runs())
	}
}

// A stop (the context, SIGTERM to the supervisor) stops the service with
// SIGTERM and ends without a restart.
func TestSupervisorStopsItsService(t *testing.T) {
	dir := t.TempDir()
	prog := filepath.Join(dir, "prog")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\ntrap 'echo term > \"$1\"; exit 0' TERM\nwhile :; do sleep 0.05; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mark := filepath.Join(dir, "mark")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool)
	started := make(chan struct{})
	go func() {
		done <- Supervisor{Command: func() *exec.Cmd { close(started); return exec.Command(prog, mark) }}.Run(ctx)
	}()
	<-started
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case gaveUp := <-done:
		if gaveUp {
			t.Fatal("a stop counted as giving up")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the supervisor did not stop")
	}
	if b, _ := os.ReadFile(mark); strings.TrimSpace(string(b)) != "term" {
		t.Fatalf("the service got no SIGTERM: %q", b)
	}
}
