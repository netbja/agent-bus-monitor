package notify

import "path/filepath"

// StateDir is <state>, the folder of notify-last.json:
// $XDG_STATE_HOME/agentbus, else ~/.local/state/agentbus, else "" when
// neither variable holds an absolute path. agentbus and busmon both read it.
func StateDir(getenv func(string) string) string {
	if d := getenv("XDG_STATE_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "agentbus")
	}
	if home := getenv("HOME"); filepath.IsAbs(home) {
		return filepath.Join(home, ".local", "state", "agentbus")
	}
	return ""
}
