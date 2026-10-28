package service

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// envKeys are copied from the shell that runs `agentbus up` into the
// services' environment file: the broker connection, the herdr session the
// dispatcher talks to, and what the conductor's test commands need to find
// their tools. A pane's own identity (HERDR_PANE_ID, HERDR_TAB_ID, …) is
// never carried: a service is not in a pane.
var envKeys = []string{
	"PATH", "LANG", "LC_ALL", "TZ",
	"REDIS_URL", "REDIS_HOST", "REDIS_PORT", "REDIS_PASSWORD",
	"HERDR_SESSION", "HERDR_SOCKET_PATH", "HERDR_BIN", "HERDR_BIN_PATH",
	"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME",
	"GOPATH", "GOCACHE", "GOMODCACHE", "GOTOOLCHAIN", "GOFLAGS",
}

// EscalateEnv names the agent the project's services escalate to (default
// master), recorded by `agentbus up --escalate-to`.
const EscalateEnv = "AGENT_BUS_ESCALATE_TO"

// ReloadEnv is the project's reload policy (lifecycle part C): unset (on),
// propose or off. `agentbus up --reload` records it; `agentbus reload` reads
// it from the caller's environment, else from this file.
const ReloadEnv = "AGENT_BUS_RELOAD"

// envPrefixes are copied whole, except the names below.
var envPrefixes = []string{"AGENT_BUS_"}

var envSkip = map[string]bool{"AGENT_BUS_PROJECT": true, "AGENT_BUS_AGENT": true}

// CollectEnv picks the service environment out of a process environment.
func CollectEnv(environ []string) map[string]string {
	out := map[string]string{}
	keep := map[string]bool{}
	for _, k := range envKeys {
		keep[k] = true
	}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || envSkip[k] {
			continue
		}
		take := keep[k]
		for _, p := range envPrefixes {
			take = take || strings.HasPrefix(k, p)
		}
		if take {
			out[k] = v
		}
	}
	return out
}

// WriteEnvFile writes env as KEY="value" lines, 0600 in a 0700 directory,
// replacing the file atomically. It reports whether the content changed.
func WriteEnvFile(path string, env map[string]string) (bool, error) {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# agentbus project services: written by `agentbus up`, read by `agentbus service run`.\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, strconv.Quote(env[k]))
	}
	if old, err := os.ReadFile(path); err == nil && string(old) == b.String() {
		return false, nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(dir, ".env-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return false, err
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), path)
}

// ErrEnvPermissions: the environment file is readable by others. It may hold
// the broker password, so a service refuses it.
var ErrEnvPermissions = errors.New("environment file is readable by group or others (chmod 600)")

// LoadEnvFile reads a file written by WriteEnvFile.
func LoadEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s: %w", path, ErrEnvPermissions)
	}
	env := map[string]string{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, q, ok := strings.Cut(line, "=")
		v, err := strconv.Unquote(q)
		if !ok || err != nil || k == "" {
			return nil, fmt.Errorf("%s:%d: not KEY=\"value\"", path, n)
		}
		env[k] = v
	}
	return env, sc.Err()
}
