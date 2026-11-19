package cmdtest

import (
	"os/exec"
	"strings"
	"testing"
)

// The test helpers are for tests only: no binary of the module links them.
// bus's board guard skips internal/cmdtest on that ground (a fixture seeds a
// board entry raw, as the _test.go files it came from could).
func TestNoBinaryLinksTheTestHelpers(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	cmd := exec.Command("go", "list", "-deps", "./cmd/...")
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, p := range strings.Fields(string(out)) {
		if strings.HasSuffix(p, "/internal/cmdtest") || strings.HasSuffix(p, "/internal/bustest") {
			t.Errorf("%s is linked into a binary", p)
		}
	}
	if !strings.Contains(string(out), "/bus\n") {
		t.Fatalf("go list listed no module package:\n%s", out)
	}
}
