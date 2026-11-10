package rolecfg

import (
	"os"
	"path/filepath"

	"github.com/netbja/agent-bus-monitor/buildinfo"
)

// ManifestPath is roles.toml as the launcher finds it: ROLES_TOML, else
// <repo>/roles.toml, where the repo is AGENTBUS_REPO, else the checkout
// setup.sh stamped (buildinfo.Repo) when it holds a roles.toml, else this
// binary's directory (the scripts run <repo>/agentbus). An empty variable is
// unset. The path is absolute, its links resolved when it exists. agentbus
// and busmon (the @ picker's shared denies) read the same file.
func ManifestPath(getenv func(string) string) (string, error) {
	path := getenv("ROLES_TOML")
	if path == "" {
		repo := getenv("AGENTBUS_REPO")
		if repo == "" && buildinfo.Repo != "" {
			// The checkout setup.sh stamped, as repoDir finds scripts/bootstrap:
			// an installed binary has no roles.toml beside it.
			if st, err := os.Stat(filepath.Join(buildinfo.Repo, "roles.toml")); err == nil && !st.IsDir() {
				repo = buildinfo.Repo
			}
		}
		if repo == "" {
			exe, err := os.Executable()
			if err != nil {
				return "", err
			}
			if r, err := filepath.EvalSymlinks(exe); err == nil {
				exe = r
			}
			repo = filepath.Dir(exe)
		}
		path = filepath.Join(repo, "roles.toml")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	return abs, nil
}
