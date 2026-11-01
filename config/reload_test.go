package config

import "testing"

// sentinel.reload (auto-reload): the reload policy, AGENT_BUS_RELOAD's
// rule kept byte for byte: propose and off are themselves, any other set
// value reads on, as agentbus reload has always read it; in the file only
// on, propose or off is a setting.
func TestSentinelReload(t *testing.T) {
	k, ok := Lookup("sentinel.reload")
	if !ok || k.Env != "AGENT_BUS_RELOAD" || k.Kind != String || k.Default != "on" || k.Legacy {
		t.Fatalf("row %+v", k)
	}
	for raw, want := range map[string]string{"on": "on", "propose": "propose", "off": "off", "yes": "on", "OFF": "on"} {
		if v, res := k.env(raw); res != envUse || v != want {
			t.Errorf("env %q = %v %v, want %q", raw, v, res, want)
		}
	}
	for v, ok := range map[any]bool{"on": true, "propose": true, "off": true, "yes": false, true: false} {
		got, why := k.file(v)
		if ok != (why == "") || ok && got != v {
			t.Errorf("file %#v = %#v %q", v, got, why)
		}
	}
	if _, why := k.file("yes"); why != "want on, propose or off" {
		t.Errorf("file refusal %q", why)
	}
	p := writeFile(t, "config.toml", "[sentinel]\nreload = \"propose\"\n")
	files := []*File{Load(p, "config.toml")}
	if got := (Resolver{Getenv: env(nil), Files: files}).Get("sentinel.reload"); got.Value != "propose" || got.Source != "config.toml" {
		t.Errorf("file: %+v", got)
	}
	if got := (Resolver{Getenv: env(map[string]string{"AGENT_BUS_RELOAD": "off"}), Files: files}).Get("sentinel.reload"); got.Value != "off" || got.Source != "env AGENT_BUS_RELOAD" {
		t.Errorf("env over file: %+v", got)
	}
}
