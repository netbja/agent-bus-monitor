//go:build darwin

package dispatch

import (
	"strings"
	"testing"
)

// macOS's boot id is the boot session uuid (kern.bootsessionuuid): one per
// boot, stable within it (self-repair step 10).
func TestBootIDDarwin(t *testing.T) {
	a, err := BootID()
	if err != nil || len(a) != 36 || strings.TrimSpace(a) != a {
		t.Fatalf("BootID = %q, %v", a, err)
	}
	if b, _ := BootID(); b != a {
		t.Fatalf("two reads differ: %q %q", a, b)
	}
}
