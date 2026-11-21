package config

import "testing"

// person.name (busmon-cli-flags item 3, the person's option A): the person's
// own name, a bus name, empty by default, beside person.language; busmon's
// identity falls back to it, and master's skill may read it.
func TestPersonNameKey(t *testing.T) {
	k, ok := Lookup("person.name")
	if !ok {
		t.Fatal("person.name not registered")
	}
	if k.Kind != String || k.Default != "" || k.Env != "AGENT_BUS_PERSON_NAME" || k.Group() != GroupIdentity || k.Legacy {
		t.Fatalf("%+v group %q", k, k.Group())
	}
	for _, good := range []string{"charlie", "ana-maria", "j_2"} {
		if v, res := k.env(good); res != envUse || v != good {
			t.Errorf("env %q: %v %v", good, v, res)
		}
		if v, why := k.file(good); why != "" || v != good {
			t.Errorf("file %q: %v %q", good, v, why)
		}
	}
	if v, why := k.file(""); why != "" || v != "" {
		t.Errorf("file empty: %v %q", v, why)
	}
	for _, bad := range []string{"Charlie", "jane doe", "1abc", "-x"} {
		if _, res := k.env(bad); res != envInvalid {
			t.Errorf("env %q: %v, want refused", bad, res)
		}
		if _, why := k.file(bad); why == "" {
			t.Errorf("file %q taken", bad)
		}
	}
}
