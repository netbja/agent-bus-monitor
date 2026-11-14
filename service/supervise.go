package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"
)

// Supervisor runs one project service where no service manager does
// (services.manager = none; herdr exit spec 4.4b), with the holder units'
// policy (4.4a): a non-zero exit is restarted after 2 s, at most 5 starts in
// 300 s, then it gives up; an exit 0 is final. AfterExit runs after each
// exit, before any restart: for the holder, the kill of the process groups it
// recorded (there is no cgroup to do it).
type Supervisor struct {
	Command   func() *exec.Cmd
	AfterExit func(code int) // nil: nothing
	Log       io.Writer      // nil: discarded
	Now       func() time.Time
	// Sleep waits d, or until ctx ends (false).
	Sleep func(ctx context.Context, d time.Duration) bool
}

// The policy, the holder units' own (spec 4.4a).
const (
	SuperviseRestartDelay = holderRestartSec * time.Second
	SuperviseWindow       = holderStartInterval * time.Second
	SuperviseBurst        = holderStartBurst
)

// Run supervises until the service exits 0 or ctx ends (a stop: the service
// gets SIGTERM and is waited for) — false — or the burst is spent — true.
func (s Supervisor) Run(ctx context.Context) (gaveUp bool) {
	now, sleep, log := s.Now, s.Sleep, s.Log
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) bool {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
				return true
			case <-ctx.Done():
				return false
			}
		}
	}
	if log == nil {
		log = io.Discard
	}
	// spent: a start at t would be one more than the burst within the window.
	var starts []time.Time
	spent := func(t time.Time) bool {
		kept := starts[:0]
		for _, st := range starts {
			if t.Sub(st) < SuperviseWindow {
				kept = append(kept, st)
			}
		}
		starts = kept
		return len(starts) >= SuperviseBurst
	}
	for {
		if ctx.Err() != nil {
			return false
		}
		// Judged at the start's real time, after the wait: a wait that
		// overran may have let the oldest start leave the window
		// (reviewer-codex).
		if spent(now()) {
			fmt.Fprintf(log, "supervise: gave up: %d starts in %v\n", SuperviseBurst, SuperviseWindow)
			return true
		}
		starts = append(starts, now())
		code, err := s.runOnce(ctx)
		if s.AfterExit != nil {
			s.AfterExit(code)
		}
		switch {
		case ctx.Err() != nil:
			fmt.Fprintf(log, "supervise: stopped\n")
			return false
		case err != nil:
			fmt.Fprintf(log, "supervise: %v\n", err)
		case code == 0:
			fmt.Fprintf(log, "supervise: the service exited 0: done\n")
			return false
		default:
			fmt.Fprintf(log, "supervise: the service exited %d\n", code)
		}
		fmt.Fprintf(log, "supervise: restart in %v\n", SuperviseRestartDelay)
		if !sleep(ctx, SuperviseRestartDelay) {
			return false
		}
	}
}

// runOnce starts the service and waits for it; ctx ending sends it SIGTERM.
// A program that cannot start is an error (counted as a failed start).
func (s Supervisor) runOnce(ctx context.Context) (int, error) {
	c := s.Command()
	if err := c.Start(); err != nil {
		return -1, err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = c.Process.Signal(syscall.SIGTERM)
		case <-done:
		}
	}()
	err := c.Wait()
	close(done)
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &ee):
		return ee.ExitCode(), nil // -1 for a signal
	}
	return -1, err
}
