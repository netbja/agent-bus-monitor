package rolecfg

import "path/filepath"

// ProjectsDir is where bootstrap writes and `agentbus up` reads the project
// templates: AGENTBUS_PROJECTS_DIR, else $XDG_CONFIG_HOME/agentbus/projects,
// else ~/.config/agentbus/projects. There is deliberately no fallback to the
// old herdr-plus directory: templates are moved there by hand, once.
func ProjectsDir(env func(string) string) string {
	if dir := env("AGENTBUS_PROJECTS_DIR"); dir != "" {
		return dir
	}
	cfg := env("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(env("HOME"), ".config")
	}
	return filepath.Join(cfg, "agentbus", "projects")
}
