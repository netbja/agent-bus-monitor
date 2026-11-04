package buildinfo

import (
	"os"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.11.1", "v0.11.0", true}, {"v0.12.0", "v0.11.9", true}, {"v1.0.0", "v0.99.99", true},
		{"v0.11.0", "v0.11.0", false}, {"v0.10.9", "v0.11.0", false},
		{"v0.11.1", "v0.11.0-3-gabc", true}, {"v0.11.0", "v0.11.0-3-gabc", false},
		{"v0.11.1", "dev", false}, {"dev", "v0.1.0", false}, {"0.11.1", "v0.11.0", false}, {"v0.11", "v0.10.0", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// about-credits: the About page's author and licence are LICENSE's own, so a
// changed LICENSE fails here rather than showing something false.
func TestLicenseMatches(t *testing.T) {
	b, err := os.ReadFile("../LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.HasPrefix(text, License+" License\n") {
		t.Fatalf("LICENSE does not open with %q", License+" License")
	}
	if !strings.Contains(text, "\n"+Copyright()+"\n") {
		t.Fatalf("LICENSE has no line %q", Copyright())
	}
}
