package main

import (
	"strings"
	"testing"
)

func palNames(t *testing.T, idx []int) []string {
	t.Helper()
	var out []string
	for _, i := range idx {
		out = append(out, paletteTable[i].Name)
	}
	return out
}

// The scorer (palette spec §4.2): exact name or alias, then a name prefix,
// then a word start in the name or summary, then a subsequence; ties keep
// the table's order; an empty query lists every command in order.
func TestPaletteMatch(t *testing.T) {
	all := palNames(t, paletteMatch(""))
	if len(all) != len(paletteTable) || all[0] != "requests" {
		t.Fatalf("empty query: %v", all)
	}
	first := func(q string) string {
		got := palNames(t, paletteMatch(q))
		if len(got) == 0 {
			return ""
		}
		return got[0]
	}
	for q, want := range map[string]string{
		"req":      "requests", // an alias, exact
		"f2":       "requests",
		"r":        "requests", // a one-letter alias, case-sensitive
		"R":        "recap",
		"t":        "tour",
		"T":        "triage",
		"u":        "unread",
		"U":        "tokens",
		"a":        "all",
		"A":        "about",
		"hea":      "health", // a name prefix
		"HEA":      "health", // case ignored beyond one letter
		"rq":       "requests",
		"config":   "settings",
		"exit":     "quit",
		"version":  "about",
		"settings": "settings",
	} {
		if got := first(q); got != want {
			t.Errorf("%q: first %q, want %q", q, got, want)
		}
	}
	// A prefix ranks before a subsequence; a word of the summary is found.
	if got := palNames(t, paletteMatch("s")); got[0] != "services" {
		t.Errorf("s: %v", got)
	}
	if got := palNames(t, paletteMatch("zzzq")); len(got) != 0 {
		t.Errorf("no match: %v", got)
	}
}

// The command line (§4.1, §4.4): a name or an alias, then its arguments,
// checked before anything runs.
func TestParseCommandLine(t *testing.T) {
	for _, c := range []struct {
		line, name string
		args       []string
		err        string
	}{
		{"requests", "requests", nil, ""},
		{"f2", "requests", nil, ""},
		{"R", "recap", nil, ""},
		{"  req  ", "requests", nil, ""},
		{"write coder hello there", "write", []string{"coder", "hello there"}, ""},
		{"@ coder hi", "write", []string{"coder", "hi"}, ""},
		{"master please review", "master", []string{"please review"}, ""},
		{"filter coder build", "filter", []string{"coder build"}, ""},
		{"filter", "filter", nil, ""},
		{"set display.theme", "set", []string{"display.theme"}, ""},
		{"set", "set", nil, ""},
		{"tour", "tour", nil, ""},
		{"requests now", "", nil, "takes no argument"},
		{"write", "", nil, "agent"},
		{"write Coder hi", "", nil, "not an agent name"},
		{"write coder", "", nil, "text"},
		{"master", "", nil, "text"},
		{"set no.such.key", "", nil, "no setting"},
		{"nosuch", "", nil, "no command"},
		{"", "", nil, "no command"},
	} {
		cmd, args, err := parseCommandLine(c.line)
		switch {
		case c.err != "":
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err %v, want %q", c.line, err, c.err)
			}
		case err != nil || cmd == nil || cmd.Name != c.name || strings.Join(args, "|") != strings.Join(c.args, "|"):
			name := ""
			if cmd != nil {
				name = cmd.Name
			}
			t.Errorf("%q: %q %q %v", c.line, name, args, err)
		}
	}
}

// The registry guard (§5): every Act and Key names an action bound on the
// panels, and every name and alias is unique.
func TestPaletteRegistry(t *testing.T) {
	bound := map[Action]bool{}
	for _, b := range panelBindings {
		bound[b.Act] = true
	}
	seen := map[string]string{}
	for _, c := range paletteTable {
		if c.Act != ActNone && !bound[c.Act] && c.Act != ActQuit {
			t.Errorf("%s: its action is bound on no panel key", c.Name)
		}
		if c.Key != ActNone && !bound[c.Key] {
			t.Errorf("%s: its key's action is bound on no panel key", c.Name)
		}
		if (c.Act == ActNone) == (c.Run == nil) {
			t.Errorf("%s: an Act or a Run, exactly one", c.Name)
		}
		for _, n := range append([]string{c.Name}, c.Aliases...) {
			if other, dup := seen[n]; dup {
				t.Errorf("%q names %s and %s", n, other, c.Name)
			}
			seen[n] = c.Name
		}
	}
}

// The row's key hint is derived from the panel table, not typed (§4.1).
func TestPaletteKeyHints(t *testing.T) {
	hint := func(name string) string {
		for _, c := range paletteTable {
			if c.Name == name {
				return paletteKeyHint(c, false)
			}
		}
		t.Fatalf("no %s", name)
		return ""
	}
	for name, want := range map[string]string{"requests": "F2", "health": "F5", "write": "@", "filter": "f", "set": "C", "tour": "F1 t", "master": "w"} {
		if got := hint(name); !strings.Contains(got, want) {
			t.Errorf("%s: hint %q, want %q", name, got, want)
		}
	}
	if got := hint("follow"); got != "" { // a setting now, no key (palette spec §8.3)
		t.Errorf("follow: hint %q, want none", got)
	}
	for _, c := range paletteTable {
		if c.Name == "master" && paletteKeyHint(c, true) != "" {
			t.Error("master shows w while an ask is open (w then answers it)")
		}
	}
}
