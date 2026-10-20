//go:build !linux

package main

import "errors"

// shellAtPrompt cannot read a terminal's mode here: no pane is ever proven
// idle, so the relaunch reports unknown instead of typing.
func shellAtPrompt(int, string) (bool, error) {
	return false, errors.New("prompt detection needs Linux")
}
