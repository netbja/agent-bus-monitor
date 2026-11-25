package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// The release gate's update check (update-old-binary): a release must not
// break agentbus update from the previous one. That update reads the new
// tree's roles.toml: a previous release with roles skill-plan asks the new
// binary for it, so the candidate must answer; an older one parses it with
// its own strict loader, so the candidate's roles.toml must hold no key it
// does not know. Both binaries are built here: the previous release from its
// tag, the candidate from this tree. Slow, so it runs under the gate only,
// and never skips there.
func TestReleaseUpdateFromPrevious(t *testing.T) {
	if os.Getenv(e2eGate) != "1" {
		t.Skip("release gate only (" + e2eGate + "=1): builds the previous release")
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	tagOf := "HEAD"
	if out, err := gateRun(repo, nil, "git", "tag", "--points-at", "HEAD"); err == nil && strings.TrimSpace(out) != "" {
		tagOf = "HEAD^" // the release commit itself: the previous release is behind it
	}
	prev, err := gateRun(repo, nil, "git", "describe", "--tags", "--abbrev=0", "--match", "v[0-9]*", tagOf)
	if err != nil {
		t.Fatalf("the previous release: %v: %s", err, prev)
	}
	t.Run("previous release", func(t *testing.T) { updateFrom(t, repo, strings.TrimSpace(prev)) })
	// The previous release asking the new binary: this tree stands in for
	// it, until a release with roles skill-plan is the previous one.
	t.Run("a previous release with skill-plan", func(t *testing.T) { updateFrom(t, repo, "HEAD") })
}

func gateRun(dir string, env []string, name string, args ...string) (string, error) {
	c := exec.Command(name, args...)
	c.Dir, c.Env = dir, append(os.Environ(), env...)
	out, err := c.CombinedOutput()
	return string(out), err
}

// updateFrom checks the read agentbus update of the release prev makes of
// this tree's roles.toml.
func updateFrom(t *testing.T, repo, prev string) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "prev")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := gateRun(repo, nil, "sh", "-c", `git archive "$1" | tar -x -C "$2"`, "sh", prev, src); err != nil {
		t.Fatalf("archive %s: %v: %s", prev, err, out)
	}
	prevBin, candBin := filepath.Join(tmp, "agentbus-prev"), filepath.Join(tmp, "agentbus-cand")
	for _, b := range []struct{ dir, out string }{{src, prevBin}, {repo, candBin}} {
		if out, err := gateRun(b.dir, []string{"CGO_ENABLED=0"}, "go", "build", "-o", b.out, "./cmd/agentbus"); err != nil {
			t.Fatalf("build in %s: %v: %s", b.dir, err, out)
		}
	}

	home := t.TempDir()
	env := func(tree string) []string {
		return []string{"HOME=" + home, "XDG_DATA_HOME=" + filepath.Join(home, "data"), "AGENTBUS_REPO=" + tree,
			"ROLES_TOML=" + filepath.Join(tree, "roles.toml"), "AGENT_BUS_AGENT=", "AGENT_BUS_PROJECT="}
	}
	// Does the previous release ask the new binary? It has skill-plan.
	_, askErr := gateRun(src, env(src), prevBin, "roles", "skill-plan", "--json")
	if prev == "HEAD" && askErr != nil {
		t.Fatalf("this tree has no roles skill-plan: %v", askErr)
	}
	if askErr == nil {
		out, err := gateRun(repo, env(repo), candBin, "roles", "skill-plan", "--json")
		var p rolecfg.SkillPlan
		if err != nil || json.Unmarshal([]byte(out), &p) != nil || len(p.Dirs) == 0 {
			t.Fatalf("%s's update asks this release's agentbus for its skill plan, which fails: %v\n%s", prev, err, out)
		}
		return
	}
	if out, err := gateRun(repo, env(repo), prevBin, "roles", "validate"); err != nil {
		t.Fatalf("%s's update parses this release's roles.toml with its own loader, which refuses it: %v\n%s"+
			"(a key added to roles.toml must wait one release after the update reads roles through the new binary)", prev, err, out)
	}
}
