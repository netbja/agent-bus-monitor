package dispatchsvc

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// The dispatch service reads display.date_format from config.toml at each
// message: a change applies with no restart (quota settings §2.3).
func TestServiceQuotaReadsTheDateFormatEachTime(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "agentbus", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	env := func(k string) string {
		switch k {
		case "XDG_CONFIG_HOME", "HOME":
			return dir
		}
		return ""
	}
	q := serviceQuota(env, io.Discard)
	if q.DateFormat == nil || q.DateFormat() != "relative" {
		t.Fatal("no default relative format")
	}
	if err := os.WriteFile(cfg, []byte("[display]\ndate_format = \"clock12\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := q.DateFormat(); got != "clock12" {
		t.Fatalf("after the change: %q", got)
	}
}
