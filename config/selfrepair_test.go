package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Self-repair step 3 (spec 2026-11-09-self-repair-watcher-design.md, 4.5):
// development mode is one boolean, AGENT_BUS_DEV=1 or dev = true; 0 turns
// it off, any other env value is skipped.
func TestDevMode(t *testing.T) {
	k, ok := Lookup("dev")
	if !ok || k.Kind != Bool || k.Default != false || k.Env != "AGENT_BUS_DEV" {
		t.Fatalf("dev row %+v", k)
	}
	on := Load(writeFile(t, "config.toml", "dev = true\n\n[dispatch]\nquota = true\n"), "config.toml")
	for _, c := range []struct {
		env  string
		file *File
		want bool
	}{{"", nil, false}, {"1", nil, true}, {"0", on, false}, {"yes", on, true}, {"yes", nil, false}, {"", on, true}} {
		r := Resolver{Getenv: env(map[string]string{"AGENT_BUS_DEV": c.env}), Files: []*File{c.file}}
		if got := r.Bool("dev"); got != c.want {
			t.Errorf("AGENT_BUS_DEV=%q file %v: dev %v, want %v", c.env, c.file != nil, got, c.want)
		}
	}
}

// dispatch.self_repair is on, off or shadow, on by default (the person,
// Q1). shadow is taken only in development mode: outside it, from the env
// or config.toml, it is refused with the key named, and the mode reads off
// (a refused switch never acts).
func TestSelfRepairMode(t *testing.T) {
	devFile := Load(writeFile(t, "config.toml", "dev = true\n[dispatch]\nself_repair = \"shadow\"\n"), "config.toml")
	shadowFile := Load(writeFile(t, "config.toml", "[dispatch]\nself_repair = \"shadow\"\n"), "config.toml")
	offFile := Load(writeFile(t, "config.toml", "[dispatch]\nself_repair = \"off\"\n"), "config.toml")
	for _, c := range []struct {
		name    string
		env     map[string]string
		file    *File
		want    string
		refused bool
	}{
		{"default", nil, nil, "on", false},
		{"env off", map[string]string{"AGENT_BUS_SELF_REPAIR": "off"}, nil, "off", false},
		{"file off", nil, offFile, "off", false},
		{"env shadow outside dev", map[string]string{"AGENT_BUS_SELF_REPAIR": "shadow"}, nil, "off", true},
		{"env shadow in dev", map[string]string{"AGENT_BUS_SELF_REPAIR": "shadow", "AGENT_BUS_DEV": "1"}, nil, "shadow", false},
		{"file shadow outside dev", nil, shadowFile, "off", true},
		{"file shadow, dev in the file", nil, devFile, "shadow", false},
		{"file shadow, dev off by env", map[string]string{"AGENT_BUS_DEV": "0"}, devFile, "off", true},
		{"env junk", map[string]string{"AGENT_BUS_SELF_REPAIR": "true"}, offFile, "off", true},
		// foureyes: a value the file's rule refuses never leaves the watcher on.
		{"file false", nil, Load(writeFile(t, "config.toml", "[dispatch]\nself_repair = false\n"), "config.toml"), "off", true},
		{"file Off", nil, Load(writeFile(t, "config.toml", "[dispatch]\nself_repair = \"Off\"\n"), "config.toml"), "off", true},
		{"file typo", nil, Load(writeFile(t, "config.toml", "[dispatch]\nself_repair = \"of\"\n"), "config.toml"), "off", true},
	} {
		r := Resolver{Getenv: env(c.env), Files: []*File{c.file}}
		if got := SelfRepairMode(r); got != c.want {
			t.Errorf("%s: mode %q, want %q", c.name, got, c.want)
		}
		g := r.Get("dispatch.self_repair")
		if g.Invalid != c.refused {
			t.Errorf("%s: refused %v, want %v", c.name, g.Invalid, c.refused)
		}
		if c.refused && strings.Contains(c.name, "shadow") && (!strings.Contains(g.Why, "dispatch.self_repair") || !strings.Contains(g.Why, "development mode")) {
			t.Errorf("%s: why %q does not name the key and the mode", c.name, g.Why)
		}
	}
	// The file's refusal is a warning of that file, at its line, never its value.
	r := Resolver{Getenv: env(nil), Files: []*File{shadowFile}}
	var found bool
	for _, w := range r.Warnings() {
		if w.Key == "dispatch.self_repair" && w.Line == 2 && strings.Contains(w.Msg, "development mode") {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning for shadow outside dev: %v", r.Warnings())
	}
	falseFile := Load(writeFile(t, "config.toml", "[dispatch]\nself_repair = false\n"), "config.toml")
	if ws := (Resolver{Getenv: env(nil), Files: []*File{falseFile}}).Warnings(); len(ws) != 1 || ws[0].Line != 2 || !strings.Contains(ws[0].Msg, "self-repair stays off") {
		t.Errorf("a refused line: warnings %v, want one, at line 2, saying it stays off", ws)
	}
	if ws := (Resolver{Getenv: env(nil), Files: []*File{devFile}}).Warnings(); len(ws) != 0 {
		t.Errorf("shadow in dev warned: %v", ws)
	}
}

// The budgets (the person, Q5): one relaunch per 30 min, three per 24 h,
// each above 0; and start at login, off, the machine's alone, stored only.
func TestSelfRepairBudgetsAndLogin(t *testing.T) {
	r := Resolver{Getenv: env(nil)}
	if r.Duration("dispatch.self_repair_every") != 30*time.Minute || r.Int("dispatch.self_repair_per_day") != 3 || r.Bool("services.start_at_login") {
		t.Fatal("defaults")
	}
	r = Resolver{Getenv: env(map[string]string{"AGENT_BUS_SELF_REPAIR_EVERY": "45m", "AGENT_BUS_SELF_REPAIR_PER_DAY": "5"})}
	if r.Duration("dispatch.self_repair_every") != 45*time.Minute || r.Int("dispatch.self_repair_per_day") != 5 {
		t.Fatal("env")
	}
	r = Resolver{Getenv: env(map[string]string{"AGENT_BUS_SELF_REPAIR_EVERY": "0", "AGENT_BUS_SELF_REPAIR_PER_DAY": "0"})}
	if r.Duration("dispatch.self_repair_every") != 30*time.Minute || r.Int("dispatch.self_repair_per_day") != 3 {
		t.Fatal("0 taken from the env: a budget of 0 would never relaunch, or relaunch without pause")
	}
	f := Load(writeFile(t, "config.toml", "[dispatch]\nself_repair_every = \"0\"\nself_repair_per_day = 0\n[services]\nstart_at_login = true\n"), "config.toml")
	if len(f.Warnings) != 2 {
		t.Errorf("warnings %v, want the two zero budgets", f.Warnings)
	}
	if !(Resolver{Files: []*File{f}}).Bool("services.start_at_login") {
		t.Error("start_at_login from the file")
	}
	if k, _ := Lookup("services.start_at_login"); k.Env != "" || !k.MachineOnly {
		t.Errorf("start_at_login %+v: want the machine's file only", k)
	}
}

// dev is a top-level key: Set writes it above the first table, Unset removes it.
func TestSetTopLevelKey(t *testing.T) {
	for _, body := range []string{"", "[dispatch]\nquota = true\n", "# mine\nother = 1\n\n[dispatch]\nquota = true\n", "other = 1"} {
		p := writeFile(t, "config.toml", body)
		if err := Set(p, "dev", true); err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		if f := Load(p, "config.toml"); !f.Has("dev") || !(Resolver{Files: []*File{f}}).Bool("dev") {
			t.Fatalf("%q: dev not read back", body)
		}
		if err := Set(p, "dev", false); err != nil {
			t.Fatalf("%q: second set: %v", body, err)
		}
		if err := Unset(p, "dev"); err != nil {
			t.Fatalf("%q: unset: %v", body, err)
		}
		got, _ := os.ReadFile(p)
		if strings.Contains(string(got), "dev") || (strings.Contains(body, "quota") && !strings.Contains(string(got), "quota = true")) {
			t.Errorf("%q: after unset:\n%s", body, got)
		}
	}
}
