package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Last used (project-switch note §2.5): the project busmon last opened
// against a broker, on this machine; written at start, so a crash or a
// closed terminal still records it. One line per broker, "<broker>
// <project>", in busmon's cache directory, replaced by copy and rename.

// brokerKey names the broker the client really reached: bus.Connect prefers
// REDIS_URL over -H and REDIS_HOST, so only its options know. Network,
// address, database, and tls when the connection uses it; never a username
// or a password.
func brokerKey(o *redis.Options) string {
	network := o.Network
	if network == "" {
		network = "tcp"
	}
	k := fmt.Sprintf("%s %s db%d", network, o.Addr, o.DB)
	if o.TLSConfig != nil {
		k += " tls"
	}
	return k
}

func lastUsedPath() string { return filepath.Join(filepath.Dir(busmonCachePath("x")), "last-project") }

// readLastUsed is broker's last project ("" none).
func readLastUsed(path, broker string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if k, p, ok := cutLast(line); ok && k == broker {
			return p
		}
	}
	return ""
}

// writeLastUsed records project for broker, keeping the other brokers'.
func writeLastUsed(path, broker, project string) error {
	lines := map[string]string{broker: project}
	if b, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if k, p, ok := cutLast(line); ok && k != broker {
				lines[k] = p
			}
		}
	}
	keys := make([]string, 0, len(lines))
	for k := range lines {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out strings.Builder
	for _, k := range keys {
		out.WriteString(k + " " + lines[k] + "\n")
	}
	return writeFileAtomic(path, []byte(out.String()))
}

// cutLast splits a line at its last space: the broker key holds spaces, a
// project name never does.
func cutLast(line string) (string, string, bool) {
	i := strings.LastIndexByte(line, ' ')
	if i <= 0 || i == len(line)-1 {
		return "", "", false
	}
	return line[:i], line[i+1:], true
}

// startProject is the project busmon opens (project-switch note §2.5):
// --project, then AGENT_BUS_PROJECT, then last used when it still has keys
// on this broker; "" when none (busmon then says how to name one).
func startProject(flag, env, last string, hasKeys func(string) (bool, error)) string {
	switch {
	case flag != "":
		return flag
	case env != "":
		return env
	case last != "":
		if ok, err := hasKeys(last); err == nil && ok {
			return last
		}
	}
	return ""
}
