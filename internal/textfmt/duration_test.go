package textfmt

import (
	"testing"
	"time"
)

// RunDuration: hours and minutes, the minutes left out on the hour, 0m under
// one (dispatchsvc P6, moved from cmd/agentbus run.go's runDuration).
func TestRunDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "0m", 59 * time.Second: "0m", 45 * time.Minute: "45m", 3 * time.Hour: "3h",
		3*time.Hour + 10*time.Minute: "3h10m", 26*time.Hour + time.Minute: "26h1m",
	} {
		if got := RunDuration(d); got != want {
			t.Errorf("RunDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
