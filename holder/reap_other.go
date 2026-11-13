//go:build !linux

package holder

// No subreaper outside Linux (spec 4.2's residual risk: on macOS an orphan
// reparents to launchd and leaves the tree).
func becomeSubreaper() error { return nil }

func reapLoop(stop <-chan struct{}, _ func(int) bool) { <-stop }
