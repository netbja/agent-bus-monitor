package config

import (
	"testing"
	"time"
)

func TestFormat(t *testing.T) {
	for v, want := range map[any]string{
		5 * time.Minute: "5m", 8 * time.Hour: "8h", 90 * time.Minute: "1h30m", 30 * time.Second: "30s",
		time.Duration(0): "0", 70.0: "70", 99.5: "99.5", 25: "25", true: "true", "": `""`, "dark": "dark",
	} {
		if got := Format(v); got != want {
			t.Errorf("Format(%#v) = %q, want %q", v, got, want)
		}
	}
}
