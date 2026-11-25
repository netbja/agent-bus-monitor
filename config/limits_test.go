package config

import (
	"strings"
	"testing"
	"time"
)

// Cap C's defaults (agent-resource-limits §6, §8; the person's Q4: 8G, 30m,
// no CPU cap): the person's alone, per host, from config.toml only, since an
// agent cannot raise its own cap. "off" for no memory limit, "0" for no time
// limit, 0 for no CPU cap.
func TestCommandLimitKeys(t *testing.T) {
	for _, c := range []struct {
		name string
		kind Kind
		def  any
		good []any
		bad  []any
	}{
		{"limits.command_memory", String, "8G", []any{"off", "4G", "512M"}, []any{"", "70%", "0", "4GB", int64(4)}},
		{"limits.command_time", Duration, 30 * time.Minute, []any{"5m", "0"}, []any{"-1m", "x", int64(5)}},
		{"limits.command_cpu", Int, 0, []any{int64(0), int64(200)}, []any{int64(-1), "200%"}},
	} {
		k, ok := Lookup(c.name)
		if !ok {
			t.Fatalf("%s not registered", c.name)
		}
		if k.Env != "" || !k.MachineOnly || !k.PersonOnly || k.Kind != c.kind || k.Default != c.def ||
			k.Group() != GroupServices || k.Applies() != AppliesRun {
			t.Errorf("%s row %+v (group %q, applies %v)", c.name, k, k.Group(), k.Applies())
		}
		p := writeFile(t, "config.toml", "[limits]\n")
		if err := Set(p, c.name, "1"); err == nil || !strings.Contains(err.Error(), "edit config.toml yourself") {
			t.Errorf("Set %s: %v", c.name, err)
		}
		for _, g := range c.good {
			if _, why := k.file(g); why != "" {
				t.Errorf("%s = %#v refused: %q", c.name, g, why)
			}
		}
		for _, b := range c.bad {
			if _, why := k.file(b); why == "" {
				t.Errorf("%s = %#v taken", c.name, b)
			}
		}
	}
	p := writeFile(t, "config.toml", "[limits]\ncommand_memory = \"off\"\ncommand_time = \"1h\"\ncommand_cpu = 150\n")
	r := Resolver{Getenv: env(map[string]string{}), Files: []*File{Load(p, "config.toml")}}
	if r.String("limits.command_memory") != "off" || r.Duration("limits.command_time") != time.Hour || r.Int("limits.command_cpu") != 150 {
		t.Fatalf("resolved %q %v %d", r.String("limits.command_memory"), r.Duration("limits.command_time"), r.Int("limits.command_cpu"))
	}
}
