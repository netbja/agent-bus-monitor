package notify

import "testing"

// StateDir is the folder of notify-last.json, as both binaries read it: an
// absolute XDG_STATE_HOME first, else an absolute HOME's .local/state, else
// none (cmd-0b, the one copy of agentbus' and busmon's notifyStateDir).
func TestStateDir(t *testing.T) {
	for _, c := range []struct{ xdg, home, want string }{
		{"/x/state", "/home/u", "/x/state/agentbus"},
		{"", "/home/u", "/home/u/.local/state/agentbus"},
		{"relative", "/home/u", "/home/u/.local/state/agentbus"},
		{"", "relative", ""},
		{"", "", ""},
	} {
		env := map[string]string{"XDG_STATE_HOME": c.xdg, "HOME": c.home}
		if got := StateDir(func(k string) string { return env[k] }); got != c.want {
			t.Errorf("StateDir(xdg %q, home %q) = %q, want %q", c.xdg, c.home, got, c.want)
		}
	}
}
