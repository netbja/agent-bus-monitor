package loginexpiry

import (
	"bytes"
	"errors"
	"os/exec"
	"time"
)

// Runner runs a command and returns its output and exit status (-1 when it
// could not run). Production's is RunCommand; tests pass a fake security.
type Runner func(name string, args ...string) (out []byte, exit int)

// RunCommand runs name with args, its standard output only.
func RunCommand(name string, args ...string) ([]byte, int) {
	out, err := exec.Command(name, args...).Output()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		return nil, ee.ExitCode()
	case err != nil:
		return nil, -1
	}
	return out, 0
}

// keychainVerified: the Keychain's service name for Claude Code's
// credentials is verified on a Mac (§3.1). Until then every macOS read is
// KeychainUnverified, and nothing is run.
var keychainVerified = false

// defaultService is the service name believed to hold the default config
// directory's credentials ("Claude Code-credentials"); another
// CLAUDE_CONFIG_DIR is believed to carry a suffix, not yet known.
const defaultService = "Claude Code-credentials"

// ReadKeychain reads the expiry from the Keychain entry of an agent's
// config directory ("" the default): `security find-generic-password -s
// <service> -w`, read-only, its output parsed for the one field like the
// file. A directory other than the default has no verified service name.
func ReadKeychain(run Runner, configDir string, now time.Time) (time.Time, Reason) {
	if !keychainVerified || configDir != "" {
		return time.Time{}, KeychainUnverified
	}
	out, code := run("security", "find-generic-password", "-s", defaultService, "-w")
	if code != 0 || len(bytes.TrimSpace(out)) == 0 {
		return time.Time{}, KeychainExit(code)
	}
	if len(out) > MaxSize {
		return time.Time{}, TooLarge
	}
	return parse(out, now)
}

// Read is the expiry of an agent's credentials on goos: the Keychain on
// darwin, the file under its config directory ("" = <home>/.claude)
// elsewhere.
func Read(goos, configDir, home string, run Runner, now time.Time) (time.Time, Reason) {
	if goos == "darwin" {
		return ReadKeychain(run, configDir, now)
	}
	return ReadFile(CredentialsFile(configDir, home), now)
}
