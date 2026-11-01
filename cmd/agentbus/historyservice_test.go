package main

import (
	"path/filepath"
	"testing"
)

func TestServiceHistoryReadsItsKeys(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	home := t.TempDir()
	base := map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, "cfg")}
	c := serviceHistory(env(base), "/state/p")
	if c.Dir != "/state/p/history" || c.KeepDays != 90 || c.MaxBytes != 256<<20 {
		t.Fatalf("defaults: %+v", c)
	}
	base["AGENT_BUS_HISTORY_DAYS"], base["AGENT_BUS_HISTORY_MAX_MB"] = "7", "10"
	if c := serviceHistory(env(base), "/state/p"); c.KeepDays != 7 || c.MaxBytes != 10<<20 {
		t.Fatalf("from the environment: %+v", c)
	}
	base["AGENT_BUS_HISTORY_DAYS"] = "0"
	if c := serviceHistory(env(base), "/state/p"); c.Dir != "" || c.KeepDays != 0 {
		t.Fatalf("0 = no archive: %+v", c)
	}
}
