package peer

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// ReadDenies is the role's effective Read(…) denies: the shared [tool_rules]
// merged with the role's own, exactly as the launch merges them
// (Manifest.LaunchRules). A rule it cannot read is an error: the peer then
// refuses to start, never reads with fewer denies than the role has.
func ReadDenies(m *rolecfg.Manifest, role string) ([]string, error) {
	rules, err := m.LaunchRules(role)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range rules.Deny {
		if r == "Read" {
			out = append(out, "**") // a bare Read denies every read
			continue
		}
		if !strings.HasPrefix(r, "Read(") {
			continue
		}
		if !strings.HasSuffix(r, ")") || strings.Count(r, "(") != 1 || strings.Count(r, ")") != 1 {
			return nil, fmt.Errorf("deny rule %q cannot be read", r)
		}
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(r, "Read("), ")"))
	}
	return out, nil
}

// DenyMatcher matches absolute paths against Read denies with Claude Code's
// glob meaning: ** any number of segments, * and ? within one segment, ~/
// the home directory, // an absolute path, any other path relative to the
// project root, ** at the start anywhere, and a pattern without a slash
// (*.pem) at any depth, as gitignore and Claude Code read it.
type DenyMatcher struct{ patterns [][]string }

// NewDenyMatcher compiles patterns; one it cannot read is an error. A ~/
// pattern is compiled for the home as given and, when it differs, as
// resolved: the tools compare resolved paths, and a home under a symlink
// (/home -> /var/home) would otherwise never match (SEC-LLM-6).
func NewDenyMatcher(patterns []string, root, home string) (DenyMatcher, error) {
	var m DenyMatcher
	homes := []string{home}
	if h, err := filepath.EvalSymlinks(home); err == nil && h != home {
		homes = append(homes, h)
	}
	homeOK := filepath.IsAbs(home)
	for _, p := range patterns {
		var abs []string
		switch {
		case p == "":
			return m, fmt.Errorf("an empty deny rule")
		case strings.HasPrefix(p, "**"):
			abs = []string{"/" + p}
		case strings.HasPrefix(p, "~/") && !homeOK:
			// Joined to an empty or relative home it would match nothing
			// under the root's absolute paths (reviewer-codex): fail closed.
			return m, fmt.Errorf("deny rule %q needs the home directory, and HOME is not an absolute path", p)
		case strings.HasPrefix(p, "~/"):
			for _, h := range homes {
				abs = append(abs, filepath.Join(h, p[2:]))
			}
		case strings.HasPrefix(p, "//"):
			abs = []string{p[1:]}
		case !strings.Contains(p, "/"):
			abs = []string{"/**/" + p}
		default:
			abs = []string{filepath.Join(root, strings.TrimPrefix(p, "/"))}
		}
		for _, a := range abs {
			segs := split(filepath.ToSlash(a))
			for _, s := range segs {
				if _, err := path.Match(s, ""); err != nil {
					return m, fmt.Errorf("deny rule %q: %v", p, err)
				}
			}
			m.patterns = append(m.patterns, segs)
		}
	}
	return m, nil
}

// Denied reports whether an absolute path is denied.
func (m DenyMatcher) Denied(p string) bool {
	segs := split(filepath.ToSlash(filepath.Clean(p)))
	for _, pat := range m.patterns {
		if matchSegs(pat, segs) {
			return true
		}
	}
	return false
}

func split(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// matchSegs matches path segments, ** standing for zero or more of them.
func matchSegs(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegs(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	ok, _ := path.Match(pat[0], segs[0])
	return ok && matchSegs(pat[1:], segs[1:])
}
