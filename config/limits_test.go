package config

import (
	"strings"
	"testing"
	"time"
)

// Cap A's keys (agent-resource-limits §3, §8; the person's Q1: 70 %, 4G of
// swap, a MemoryHigh soft throttle near 60 %): the person's alone, per host,
// from config.toml only, in systemd's syntax or "off".
func TestHolderLimitKeys(t *testing.T) {
	for name, def := range map[string]string{
		"limits.holder_memory_max":  "70%",
		"limits.holder_memory_high": "60%",
		"limits.holder_swap_max":    "4G",
	} {
		k, ok := Lookup(name)
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		if k.Env != "" || !k.MachineOnly || !k.PersonOnly || k.Kind != String || k.Default != def ||
			k.Group() != GroupServices || k.Applies() != AppliesRun {
			t.Errorf("%s row %+v (group %q, applies %v)", name, k, k.Group(), k.Applies())
		}
		p := writeFile(t, "config.toml", "[limits]\n")
		if err := Set(p, name, "50%"); err == nil || !strings.Contains(err.Error(), "edit config.toml yourself") {
			t.Errorf("Set %s: %v", name, err)
		}
		for _, good := range []string{"off", "70%", "100%", "1%", "4G", "512M", "2T", "1048576", "1.5G"} {
			if v, why := k.file(good); why != "" || v != good {
				t.Errorf("%s = %q refused: %q", name, good, why)
			}
		}
		for _, bad := range []any{"", "0", "0G", "0.0G", "0%", "101%", "070%", "70%%", "-1G", "4GB", "4GG", "G", ".5G", "4.", "1..5G", "4 G", "4\tG", "x", "4G\nExecStart=/bin/sh", "infinity", int64(4)} {
			if _, why := k.file(bad); why == "" {
				t.Errorf("%s = %#v taken", name, bad)
			}
		}
	}
	p := writeFile(t, "config.toml", "[limits]\nholder_memory_max = \"16G\"\nholder_memory_high = \"off\"\nholder_swap_max = \"1G\"\n")
	r := Resolver{Getenv: env(map[string]string{}), Files: []*File{Load(p, "config.toml")}}
	if r.String("limits.holder_memory_max") != "16G" || r.String("limits.holder_memory_high") != "off" || r.String("limits.holder_swap_max") != "1G" {
		t.Fatalf("resolved %q %q %q", r.String("limits.holder_memory_max"), r.String("limits.holder_memory_high"), r.String("limits.holder_swap_max"))
	}
}

// The share of a cap at which the holder's memory warns
// (agent-resource-limits §3.1, §8), a percentage like dispatch.context_alert;
// the person's alone, per host, read at each pass.
func TestLimitsWarnAt(t *testing.T) {
	k, ok := Lookup("limits.warn_at")
	if !ok {
		t.Fatal("limits.warn_at not registered")
	}
	if k.Env != "" || !k.MachineOnly || !k.PersonOnly || k.Kind != Float || k.Default != 80.0 ||
		k.Group() != GroupHealth || k.Applies() != AppliesLive {
		t.Errorf("row %+v (group %q, applies %v)", k, k.Group(), k.Applies())
	}
	for _, good := range []any{int64(80), 95.5, int64(100)} {
		if _, why := k.file(good); why != "" {
			t.Errorf("%#v refused: %q", good, why)
		}
	}
	for _, bad := range []any{int64(0), int64(101), -5.0, "80", 0.8 - 1} {
		if _, why := k.file(bad); why == "" {
			t.Errorf("%#v taken", bad)
		}
	}
	p := writeFile(t, "config.toml", "[limits]\nwarn_at = 90\n")
	r := Resolver{Getenv: env(map[string]string{"AGENT_BUS_LIMITS_WARN_AT": "10"}), Files: []*File{Load(p, "config.toml")}}
	if r.Float("limits.warn_at") != 90 {
		t.Fatalf("resolved %v", r.Float("limits.warn_at"))
	}
}

// How long an older holder build may drain before holder-draining fails
// (agent-resource-limits §3.2, §8): the person's, per host, read each pass.
func TestLimitsHolderDrainStaleAfter(t *testing.T) {
	k, ok := Lookup("limits.holder_drain_stale_after")
	if !ok {
		t.Fatal("not registered")
	}
	if k.Env != "" || !k.MachineOnly || !k.PersonOnly || k.Kind != Duration || k.Default != 6*time.Hour ||
		k.Group() != GroupHealth || k.Applies() != AppliesLive {
		t.Errorf("row %+v (group %q, applies %v)", k, k.Group(), k.Applies())
	}
	for _, bad := range []any{"0", "-1h", "x", int64(6)} {
		if _, why := k.file(bad); why == "" {
			t.Errorf("%#v taken", bad)
		}
	}
	if v, why := k.file("12h"); why != "" || v != 12*time.Hour {
		t.Errorf("12h: %v %q", v, why)
	}
}
