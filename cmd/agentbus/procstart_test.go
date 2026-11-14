package main

import "testing"

// The dispatcher's process identity exists where the system lists processes
// with a start time: Linux (/proc) and, since self-repair step 10, macOS
// (sysctl). Elsewhere a snapshot proves nothing and the panes decide.
func TestProcStartFnFor(t *testing.T) {
	for goos, want := range map[string]bool{"linux": true, "darwin": true, "freebsd": false, "windows": false} {
		if got := procStartFnFor(goos) != nil; got != want {
			t.Errorf("%s: identity %v, want %v", goos, got, want)
		}
	}
}
