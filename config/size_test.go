package config

import "testing"

func TestParseSize(t *testing.T) {
	for s, want := range map[string]int64{"1": 1, "1048576": 1 << 20, "512M": 512 << 20, "8G": 8 << 30, "1.5G": 3 << 29, "2T": 2 << 40, "4K": 4096} {
		if got, err := ParseSize(s); err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d %v, want %d", s, got, err, want)
		}
	}
	for _, s := range []string{"", "0", "0G", "-1G", "G", "4GB", "4g", "4 G", "1.G", ".5G", "1..5G", "70%", "off", "x", "4G\n", "99999999T"} {
		if got, err := ParseSize(s); err == nil {
			t.Errorf("ParseSize(%q) = %d, want an error", s, got)
		}
	}
}
