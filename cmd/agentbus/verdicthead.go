package main

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/netbja/agent-bus-monitor/bus"
)

// resolveVerdictHead is verdict --head's value (forge-delivery): a full SHA
// that names a commit in the reviewer's checkout dir, returned as git prints
// it. A short SHA, a ref name or one the checkout does not hold is refused:
// the head binds a delivery to exactly what the reviewer read.
func resolveVerdictHead(dir, sha string) (string, error) {
	if !bus.ValidHead(sha) {
		return "", fmt.Errorf("--head must be the full commit SHA you read (git rev-parse HEAD), not %q", sha)
	}
	c := exec.Command("git", "rev-parse", "--verify", "--quiet", sha+"^{commit}")
	c.Dir = dir
	out, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("--head %s is not a commit in this checkout", sha)
	}
	if got := strings.TrimSpace(string(out)); got != sha {
		return "", fmt.Errorf("--head %s resolves to %s here", sha, got)
	}
	return sha, nil
}
