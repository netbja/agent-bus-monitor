package secretpath

import "testing"

// The families health's tracked-secrets check has always matched, moved here
// unchanged (compose-at-file §3), and the two kinds the spec adds: a path
// under .ssh, .gnupg, .aws or .git, and agent-bus's own config files.
func TestIs(t *testing.T) {
	for _, tc := range []struct {
		path   string
		secret bool
	}{
		{".env", true}, {".env.production", true}, {".env.example", false}, {"docs/.ENV", true},
		{"id_ed25519.pub", true}, {"id_rsa", true}, {"certs/server.pem", true}, {"tls.key", true},
		{"wallet-main.json", true}, {"keypair.json", true}, {"my-keypair-2.json", true},
		{"README.md", false}, {"keys.go", false}, {"wallet.go", false}, {"environment.md", false},
		// a path under these directories, at any depth, whatever the file
		{".ssh/config", true}, {"home/x/.gnupg/pubring.kbx", true}, {".aws/credentials", true},
		{".git/config", true}, {"sub/.git/hooks/pre-commit", true}, {"vendor/.GIT/HEAD", true},
		{"docs/ssh/notes.md", false}, {".github/workflows/ci.yml", false}, {".gitignore", false},
		// agent-bus's own config files, under an agentbus config directory
		{".config/agentbus/notify.toml", true}, {"agentbus/config.toml", true},
		{"config/config.toml", false}, {"agentbus/roles.toml", false}, {"notify.toml", false},
	} {
		kind, got := Is(tc.path)
		if got != tc.secret || got && kind == "" {
			t.Errorf("Is(%q) = %q, %v; want secret=%v", tc.path, kind, got, tc.secret)
		}
	}
}
