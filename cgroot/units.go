package cgroot

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

// The scopes of the user manager (resource-limits §12.5, step 4b-2): read
// back from their names, listed and stopped. The holder stops a terminal's
// scope when the terminal ends, every scope of its build when it stops, and
// a start sweep's stale ones: a scope is outside the holder's unit, so its
// background processes would outlive the holder otherwise.

var (
	scopeNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`) // bus.ValidName's, for a project or a role
	scopeTermRE = regexp.MustCompile(`^t[0-9]+$`)
)

// ParseScopeUnit reads ScopeUnit's name back. The build key is the part
// with a dot (a project or a role never holds one, a key always does), so
// dashes in the project or the role still split one way only.
func ParseScopeUnit(unit string) (project, key, role, term string, ok bool) {
	rest, found := strings.CutPrefix(unit, "agentbus-")
	if !found {
		return "", "", "", "", false
	}
	if rest, found = strings.CutSuffix(rest, ".scope"); !found {
		return "", "", "", "", false
	}
	parts := strings.Split(rest, "-")
	k := slices.IndexFunc(parts, func(p string) bool { return strings.Contains(p, ".") })
	if k < 0 || k > len(parts)-2 { // the name checks below refuse an empty project or role, and a second dot
		return "", "", "", "", false
	}
	project, key = strings.Join(parts[:k], "-"), parts[k]
	role, term = strings.Join(parts[k+1:len(parts)-1], "-"), parts[len(parts)-1]
	if !scopeNameRE.MatchString(project) || !scopeNameRE.MatchString(role) || !scopeTermRE.MatchString(term) || ScopeKey(key) != key {
		return "", "", "", "", false
	}
	return project, key, role, term, true
}

// UserScopes is the user manager's view of agentbus scopes, through its
// systemctl (a fake in tests).
type UserScopes struct {
	Systemctl string
}

// List is every agentbus scope of project, any build, loaded in the user
// manager. A manager that cannot answer is an error, never "none".
func (u UserScopes) List(ctx context.Context, project string) ([]string, error) {
	out, err := exec.CommandContext(ctx, u.Systemctl, "--user", "list-units", "--all", "--plain", "--no-legend", "--type=scope",
		"agentbus-"+project+"-*.scope").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("cgroot: listing the scopes: %v: %s", err, strings.TrimSpace(string(out)))
	}
	var units []string
	for _, l := range strings.Split(string(out), "\n") {
		for _, f := range strings.Fields(l) {
			if !strings.HasSuffix(f, ".scope") {
				continue // a "●" before a failed unit
			}
			if p, _, _, _, ok := ParseScopeUnit(f); ok && p == project { // the glob also reaches project <project>-x
				units = append(units, f)
			}
			break
		}
	}
	return units, nil
}

// Stop stops units, which kills every process left in them; none, nothing.
func (u UserScopes) Stop(ctx context.Context, units ...string) error {
	if len(units) == 0 {
		return nil
	}
	out, err := exec.CommandContext(ctx, u.Systemctl, append([]string{"--user", "stop", "--"}, units...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cgroot: stopping %s: %v: %s", strings.Join(units, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
