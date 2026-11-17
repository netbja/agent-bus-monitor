package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/netbja/agent-bus-monitor/holder"
)

// holderProber is the part of the project's holder the dispatch service's
// heartbeat asks: its own build's holder, then every holder of the project,
// each with its terminals.
type holderProber interface {
	Ping(ctx context.Context) error
	Holders(ctx context.Context) ([]holder.HolderState, error)
}

// holderBehindError: the dispatcher's own build has no holder that answers,
// while another build's holder does (agent-screen-blocked spec §1). Agents
// on those holders are still read and woken, since those calls route by the
// terminal's build; a start (pop, relaunch, reload) goes to the dispatcher's
// own build and fails.
type holderBehindError struct {
	Own      string   // the dispatcher's build
	Others   []string // the builds whose holder answered
	Hosting  []string // those listed with a live terminal
	Unlisted []string // those whose terminals could not be listed
	err      error    // the dial of its own build's holder
}

func (e *holderBehindError) Error() string { return e.err.Error() }
func (e *holderBehindError) Unwrap() error { return e.err }

// detail is the service state's detail: only what the probe knows of each
// build (reviewer-codex: an answering holder may host no agent, and one
// whose list failed is not known to host none).
func (e *holderBehindError) detail(project string) string {
	var parts []string
	if len(e.Hosting) > 0 {
		parts = append(parts, fmt.Sprintf("agents on build %s are still woken", strings.Join(e.Hosting, ", ")))
	}
	for _, b := range e.Others {
		switch {
		case slices.Contains(e.Hosting, b):
		case slices.Contains(e.Unlisted, b):
			parts = append(parts, fmt.Sprintf("the holder of build %s answers; its agents could not be listed", b))
		default:
			parts = append(parts, fmt.Sprintf("the holder of build %s answers but hosts no agent", b))
		}
	}
	return fmt.Sprintf("no holder of this build (%s) runs: %s; a pop, a relaunch or a reload fails until agentbus up %s starts it", e.Own, strings.Join(parts, "; "), project)
}

// probeHolders is nil when the holder of build own answers; a
// holderBehindError when only another build's holder does; else the error of
// own's Ping.
func probeHolders(ctx context.Context, h holderProber, own string) error {
	err := h.Ping(ctx)
	if err == nil {
		return nil
	}
	states, herr := h.Holders(ctx)
	if herr != nil || len(states) == 0 {
		return err
	}
	behind := &holderBehindError{Own: own, err: err}
	for _, s := range states {
		if s.Build == own {
			return nil // it answered after all
		}
		behind.Others = append(behind.Others, s.Build)
		switch {
		case !s.Listed:
			behind.Unlisted = append(behind.Unlisted, s.Build)
		case s.Live > 0:
			behind.Hosting = append(behind.Hosting, s.Build)
		}
	}
	return behind
}

// serviceHostPing is the dispatch service's HostPing: the probe of every
// holder of project (newHost builds the project's holder client), not only
// its own build's Ping.
func serviceHostPing(project, own string, newHost func(project string) (holderProber, error)) func(context.Context) error {
	return func(ctx context.Context) error {
		h, err := newHost(project)
		if err != nil {
			return err
		}
		return probeHolders(ctx, h, own)
	}
}
