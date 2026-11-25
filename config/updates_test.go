package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// quiet-update-auto §6, §7 and the person's answers (Q2: the window
// editable by the person in Shift+C): updates.auto and updates.max_wait are
// the person's only (PersonOnly), updates.window is PersonEdit; all three
// machine keys (the updater is the host's), no env variable.
func TestUpdatesKeys(t *testing.T) {
	for _, c := range []struct {
		name               string
		kind               Kind
		def                any
		personOnly, person bool
	}{
		{"updates.auto", String, "notify", true, false},
		{"updates.window", String, "03:00-05:00", false, true},
		{"updates.max_wait", Duration, 7 * 24 * time.Hour, true, false},
	} {
		k, ok := Lookup(c.name)
		if !ok {
			t.Errorf("%s: not a key", c.name)
			continue
		}
		if k.Kind != c.kind || k.Default != c.def || k.Env != "" || !k.MachineOnly || k.Project ||
			k.PersonOnly != c.personOnly || k.PersonEdit != c.person || k.Group() != GroupAttention {
			t.Errorf("%s: %+v", c.name, k)
		}
		// ReadBy names the step that reads each (foureyes on wip-limit-2a):
		// updates.auto read by update --auto from step 4a, the others not yet.
		if want := map[string]string{"updates.auto": "agentbus update --auto"}[c.name]; want == "" && !strings.Contains(k.ReadBy, "quiet-update step 4") ||
			want != "" && !strings.HasPrefix(k.ReadBy, want) {
			t.Errorf("%s: ReadBy %q", c.name, k.ReadBy)
		}
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := Set(path, c.name, c.def); err == nil || !strings.Contains(err.Error(), "the person's own setting") {
			t.Errorf("%s: Set: %v", c.name, err)
		}
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := SetAsPerson(path, "updates.window", "01:30-02:15"); err != nil {
		t.Fatalf("the window as the person: %v", err)
	}
	if err := SetAsPerson(path, "updates.auto", "install"); err == nil {
		t.Fatal("updates.auto written as the person: it is config.toml's alone")
	}
	for raw, ok := range map[string]bool{"notify": true, "install": true, "off": false, "stage": false} {
		if _, why := mustKey(t, "updates.auto").file(raw); (why == "") != ok {
			t.Errorf("updates.auto %q: %q", raw, why)
		}
	}
	for raw, ok := range map[string]bool{"7d": true, "36h": true, "0": false, "-1h": false, "400d": false} {
		if _, why := mustKey(t, "updates.max_wait").file(raw); (why == "") != ok {
			t.Errorf("updates.max_wait %q: %q", raw, why)
		}
	}
}

func mustKey(t *testing.T, name string) *Key {
	t.Helper()
	k, ok := Lookup(name)
	if !ok {
		t.Fatalf("%s: not a key", name)
	}
	return k
}

// The window, HH:MM-HH:MM local (r13 §4.7): start and end minutes; a window
// across midnight is allowed; equal bounds, a bad time or form refused.
func TestParseWindow(t *testing.T) {
	for _, c := range []struct {
		raw        string
		start, end int
		ok         bool
	}{
		{"03:00-05:00", 180, 300, true},
		{"23:30-01:00", 1410, 60, true},
		{"00:00-23:59", 0, 1439, true},
		{"03:00-03:00", 0, 0, false},
		{"3:00-05:00", 0, 0, false},
		{"03:00-24:00", 0, 0, false},
		{"03:60-05:00", 0, 0, false},
		{"03:00 - 05:00", 0, 0, false},
		{"", 0, 0, false},
	} {
		s, e, ok := ParseWindow(c.raw)
		if ok != c.ok || ok && (s != c.start || e != c.end) {
			t.Errorf("%q: %d %d %v", c.raw, s, e, ok)
		}
		if _, why := mustKey(t, "updates.window").file(c.raw); (why == "") != c.ok {
			t.Errorf("file rule %q: %q", c.raw, why)
		}
	}
}
