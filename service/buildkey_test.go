package service

import (
	"strings"
	"testing"
)

// The build key (herdr exit spec 4.4, security S7): one function names the
// socket, the unit, the launchd label, the binary directory and the
// pidfiles. Lowered, reduced to [a-z0-9.], cut to 40, then "." and 10 hex
// digits of the raw id's SHA-256: two raw ids that reduce alike differ.
func TestBuildKey(t *testing.T) {
	k := BuildKey("v0.29.0-12-gABC+dirty")
	if !strings.HasPrefix(k, "v0.29.0.12.gabc.dirty.") || len(k) != len("v0.29.0.12.gabc.dirty.")+10 {
		t.Fatalf("key %q", k)
	}
	for _, pair := range [][2]string{{"v1+dirty", "v1-dirty"}, {"V1", "v1"}, {"a  b", "a_b"}} {
		a, b := BuildKey(pair[0]), BuildKey(pair[1])
		if a == b {
			t.Errorf("%q and %q share the key %q", pair[0], pair[1], a)
		}
		if a[:len(a)-11] != b[:len(b)-11] {
			t.Errorf("%q and %q should reduce alike: %q %q", pair[0], pair[1], a, b)
		}
	}
	long := BuildKey(strings.Repeat("x", 100))
	if len(long) != 40+1+10 {
		t.Fatalf("long key %d chars: %q", len(long), long)
	}
	for _, raw := range []string{"v0.29.0-12-gabc", "UPPER_case/with spaces", "", "---", "é"} {
		k := BuildKey(raw)
		if strings.ContainsAny(k, "-_/ ") || k != strings.ToLower(k) || !ValidBuildKey(k) {
			t.Errorf("BuildKey(%q) = %q", raw, k)
		}
		if k != BuildKey(raw) {
			t.Errorf("not stable: %q", raw)
		}
	}
	// "<project>.<key>" splits one way only: a project has no '.'.
	for _, p := range []string{"a", "a-b"} {
		name := p + "." + BuildKey("v1")
		proj, key, ok := strings.Cut(name, ".")
		if !ok || proj != p || key != BuildKey("v1") {
			t.Fatalf("%q splits as %q %q", name, proj, key)
		}
	}
	for _, bad := range []string{"", "a-b.0123456789", "x.012345678", "x.0123456789a", "X.0123456789", ".0123456789"} {
		if ValidBuildKey(bad) {
			t.Errorf("ValidBuildKey(%q) = true", bad)
		}
	}
}
