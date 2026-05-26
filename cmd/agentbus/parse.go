package main

import (
	"strconv"
	"time"
)

// extractFlag removes the first "--name value" pair from args and returns the
// remaining args and the value ("" if the flag is absent or has no value).
func extractFlag(args []string, name string) ([]string, string) {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			out := append(append([]string{}, args[:i]...), args[i+2:]...)
			return out, args[i+1]
		}
	}
	return args, ""
}

// extractBool removes the first "--name" flag from args and reports presence.
func extractBool(args []string, name string) ([]string, bool) {
	for i := 0; i < len(args); i++ {
		if args[i] == name {
			out := append(append([]string{}, args[:i]...), args[i+1:]...)
			return out, true
		}
	}
	return args, false
}

// genRef returns a short, sortable, unique challenge id.
func genRef() string { return strconv.FormatInt(time.Now().UnixNano(), 36) }

// parseIdle interprets subscribe/watch's optional "[idle_seconds]" positional:
// whole seconds the watcher blocks before emitting a heartbeat and exiting so it
// can be re-armed. Empty, non-numeric, or non-positive falls back to def — a
// zero/negative window would make the watcher exit instantly and busy-loop.
func parseIdle(arg string, def time.Duration) time.Duration {
	if n, err := strconv.Atoi(arg); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return def
}
