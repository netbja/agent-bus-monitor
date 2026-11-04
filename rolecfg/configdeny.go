package rolecfg

import (
	"path/filepath"

	"github.com/netbja/agent-bus-monitor/config"
)

// ConfigDenies are the denies a launch adds for the person's config.toml
// when XDG_CONFIG_HOME moves it from ~/.config/agentbus/config.toml, which
// the shipped manifest denies already (backup spec, "A person's setting
// only": backup.after_command runs with the person's rights). The path is in
// Claude Code's absolute form, "//" and the path. A path that cannot be
// resolved (no HOME, a relative XDG_CONFIG_HOME) adds nothing, and note says
// so. Defence in depth: an agent with a shell can still write the file. Edit
// covers the Write tool too: Claude Code never matches a Write(path) rule.
// notify.toml beside it (the notification targets' secrets, notifications
// spec) is denied Read as well.
func ConfigDenies(getenv func(string) string) (denies []string, note string) {
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" && !filepath.IsAbs(xdg) {
		return nil, "note: XDG_CONFIG_HOME is not absolute: only the shipped deny of ~/.config/agentbus/config.toml applies"
	}
	path := config.Path(getenv)
	if path == "" {
		return nil, "note: no HOME and no XDG_CONFIG_HOME: config.toml's place is unknown, only the shipped deny applies"
	}
	if home := getenv("HOME"); filepath.IsAbs(home) && path == filepath.Join(home, ".config", "agentbus", "config.toml") {
		return nil, ""
	}
	abs, notify := "/"+path, "/"+filepath.Join(filepath.Dir(path), "notify.toml")
	return []string{"Edit(" + abs + ")", "Read(" + notify + ")", "Edit(" + notify + ")"}, ""
}
