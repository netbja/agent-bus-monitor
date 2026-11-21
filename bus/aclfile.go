package bus

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// ACLFile is the broker's users.acl (broker-hardening step 4b, part 2,
// written by ./setup.sh acl on): one line per user of ACLRoles, its rules
// from ACLRules and its password as a SHA-256 hash (the file never holds
// one in clear), then "user default off". pw holds every user's password.
func ACLFile(pw map[string]string) ([]byte, error) {
	var b strings.Builder
	for _, role := range ACLRoles {
		p := pw[role]
		if p == "" {
			return nil, fmt.Errorf("users.acl: no password for %s", role)
		}
		sum := sha256.Sum256([]byte(p))
		b.WriteString("user " + role + " reset on #" + hex.EncodeToString(sum[:]) + " " + strings.Join(ACLRules(role), " ") + "\n")
	}
	b.WriteString("user default off\n")
	return []byte(b.String()), nil
}
