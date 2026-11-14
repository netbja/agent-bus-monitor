package config

import (
	"testing"
	"time"
)

// The holder's settings (herdr exit, step 6b2): its scrollback and the two
// submit bounds it applies (spec 4.2, 4.5), registered like every tunable.
func TestHolderKeys(t *testing.T) {
	for _, c := range []struct {
		name, env string
		def       any
		good      string
		want      any
		bad       []string
	}{
		{"holder.scrollback", "AGENT_BUS_HOLDER_SCROLLBACK", 5000, "200", 200, []string{"0", "-1", "x"}},
		{"dispatch.submit_wait", "AGENT_BUS_SUBMIT_WAIT", 2 * time.Second, "500ms", 500 * time.Millisecond, []string{"0", "-1s", "x", "2m"}},
		{"dispatch.person_quiet", "AGENT_BUS_PERSON_QUIET", 3 * time.Second, "10s", 10 * time.Second, []string{"0", "x", "2h"}},
	} {
		k, ok := Lookup(c.name)
		if !ok {
			t.Fatalf("%s not registered", c.name)
		}
		if k.Env != c.env || k.Default != c.def || k.Group() != GroupServices {
			t.Errorf("%s: env %s default %v group %q", c.name, k.Env, k.Default, k.Group())
		}
		if v, r := k.env(c.good); r != envUse || v != c.want {
			t.Errorf("%s=%s: %v %v", c.env, c.good, v, r)
		}
		for _, b := range c.bad {
			if _, r := k.env(b); r != envSkip {
				t.Errorf("%s=%s taken (%v)", c.env, b, r)
			}
		}
	}
}
