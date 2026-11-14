package config

import "testing"

// attach.prefix (herdr exit spec 4.6): f1-f12 or ctrl+<letter>, ctrl+\,
// ctrl+]; never a key a terminal or every client needs. Default f12.
func TestAttachPrefix(t *testing.T) {
	k, ok := Lookup("attach.prefix")
	if !ok || k.Default != "f12" || k.Env != "AGENT_BUS_ATTACH_PREFIX" || k.Group() == "" {
		t.Fatalf("%+v %v", k, ok)
	}
	for _, good := range []string{"f12", "f1", "f10", "ctrl+g", `ctrl+\`, "ctrl+]"} {
		if v, why := k.file(good); why != "" || v != good {
			t.Errorf("file %q: %v %q", good, v, why)
		}
		if _, r := k.env(good); r != envUse {
			t.Errorf("env %q refused", good)
		}
	}
	for _, bad := range []string{"f13", "f0", "ctrl+c", "ctrl+z", "ctrl+q", "ctrl+s", "alt+x", "F12", "ctrl+G", ""} {
		if _, why := k.file(bad); why == "" {
			t.Errorf("file %q taken", bad)
		}
		if bad != "" {
			if _, r := k.env(bad); r == envUse {
				t.Errorf("env %q taken", bad)
			}
		}
	}
}
