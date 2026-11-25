package rolecfg

import (
	"fmt"
	"slices"

	"github.com/netbja/agent-bus-monitor/bus"
)

// observer-role (the person, 2026-11-25): a role for a session agentbus
// does not launch (claude-ao, the Orchestrator), read-only on the bus:
// kind = "observer" in roles.toml and nothing else. It has no profile, tier,
// skills, tool rules or delivery: no launch path may see it, so it is taken
// out of Roles and the launch order when the manifest loads, and named by
// Observers. What it may do is the CLI's (cmd/agentbus observer guard) and,
// with the broker's ACL on, its Redis user's (bus.ACLObserver).

// KindObserver is the one non-launched kind (external-wake, #781, would add
// "external" beside it).
const KindObserver = "observer"

// splitObservers moves the kind = "observer" roles out of Roles and the
// launch order into observers, each checked: a valid name, not the system
// sender, and no key but kind. Any other kind is refused.
func (m *Manifest) splitObservers() error {
	var launched []string
	for _, role := range m.roleOrder {
		info, ok := asTable(m.Roles[role])
		kind, has := info["kind"]
		if !ok || !has {
			launched = append(launched, role)
			continue
		}
		if k, isString := kind.(string); !isString || k != KindObserver {
			return fmt.Errorf("role %s: kind must be %q (or absent, a launched role)", role, KindObserver)
		}
		if !namePattern.MatchString(role) {
			return fmt.Errorf("invalid role name")
		}
		if role == bus.SystemSender {
			return fmt.Errorf("role name %q is reserved: agentbus's own asks are sent under it", role)
		}
		if err := checkKeys(info, set("kind"), "observer role "+role+" (an observer is never launched: kind only)"); err != nil {
			return err
		}
		m.observers = append(m.observers, role)
		delete(m.Roles, role)
	}
	m.roleOrder = launched
	return nil
}

// Observers are the manifest's observer roles, in declaration order.
func (m *Manifest) Observers() []string { return append([]string(nil), m.observers...) }

// IsObserver: role is one of the manifest's observers.
func (m *Manifest) IsObserver(role string) bool { return slices.Contains(m.observers, role) }
