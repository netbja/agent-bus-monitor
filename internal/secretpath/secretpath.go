// Package secretpath is the one rule for paths that look like secrets,
// shared by health's tracked-secrets check and busmon's @ file picker
// (compose-at-file §3). It reads the path only, never the file.
package secretpath

import (
	"path/filepath"
	"strings"
)

// secretDirs are directories whose every file is a credential or a
// repository's own machinery: a path with one of them as a component is a
// secret whatever its file name.
var secretDirs = map[string]string{
	".ssh":   "SSH directory",
	".gnupg": "GnuPG directory",
	".aws":   "AWS credentials directory",
	".git":   "git directory",
}

// Is says whether path looks like a secret, and which kind. The families
// match the Read(…) denies of roles.toml's shared [tool_rules]: environment
// files (not .env.example), private keys, SSH keys, keypair and wallet JSON,
// any path under .ssh, .gnupg, .aws or .git, and agent-bus's notify.toml and
// config.toml under an agentbus config directory. Names compare
// case-insensitively, as the denies do.
func Is(path string) (string, bool) {
	clean := filepath.ToSlash(filepath.Clean(path))
	parts := strings.Split(strings.ToLower(clean), "/")
	for _, p := range parts[:len(parts)-1] {
		if kind, ok := secretDirs[p]; ok {
			return kind, true
		}
	}
	lower := parts[len(parts)-1]
	if kind, ok := secretDirs[lower]; ok { // the directory itself
		return kind, true
	}
	if len(parts) >= 2 && parts[len(parts)-2] == "agentbus" && (lower == "notify.toml" || lower == "config.toml") {
		return "agent-bus config file", true
	}
	switch {
	case lower == ".env" || strings.HasPrefix(lower, ".env.") && lower != ".env.example":
		return "environment file", true
	case strings.HasSuffix(lower, ".pem"):
		return "private-key file", true
	case strings.HasSuffix(lower, ".key"):
		return "key file", true
	case strings.HasPrefix(lower, "id_rsa") || strings.HasPrefix(lower, "id_ed25519"):
		return "SSH private-key file", true
	case strings.Contains(lower, "keypair") && strings.HasSuffix(lower, ".json"):
		return "keypair JSON", true
	case strings.HasPrefix(lower, "wallet") && strings.HasSuffix(lower, ".json"):
		return "wallet JSON", true
	}
	return "", false
}
