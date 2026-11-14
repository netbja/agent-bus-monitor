package holder

import (
	"os"
	"os/signal"
	"syscall"
)

// termSignals are the signals a terminal's programs must get with their
// default action: Close's SIGHUP (spec 4.2), and the ^C and ^\ the
// terminal's line discipline sends to its foreground group.
var termSignals = []os.Signal{syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT}

// unignoreTermSignals takes back any of termSignals this process was
// started with ignored (nohup, a runner or a shell that ignores it). An
// ignored signal is inherited through exec, so every program the holder
// starts would ignore it too: Close's SIGHUP would do nothing and each
// close would wait out the grace for its SIGKILL (holder-closehangsup-flake).
// A caught signal is reset to its default by exec, so a handler here gives
// the programs the default while the holder still ignores it (the channel
// is never read; a signal it cannot take is dropped). A signal not ignored
// is left to Go's own handling.
func unignoreTermSignals() {
	for _, s := range termSignals {
		if signal.Ignored(s) {
			signal.Notify(make(chan os.Signal, 1), s)
		}
	}
}
