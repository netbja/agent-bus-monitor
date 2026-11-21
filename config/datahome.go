package config

import "path/filepath"

// DataHome is ${XDG_DATA_HOME:-~/.local/share}: XDG_DATA_HOME when it is an
// absolute path (the XDG rule: a relative one is ignored), else
// $HOME/.local/share. agentbus keeps its data under <DataHome>/agentbus.
func DataHome(getenv func(string) string) string {
	if d := getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
		return d
	}
	return filepath.Join(getenv("HOME"), ".local", "share")
}
