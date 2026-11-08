package rolecfg

import (
	"slices"
	"strings"
	"testing"
)

// backup.after_command is the person's alone (backup spec): the shipped
// manifest denies every role Edit of ~/.config/agentbus/config.toml, which
// covers the Write tool too: Claude Code checks file tools against Edit and
// Read rules only, and a Write(path) rule never matches (its permissions
// docs, "Write(path) ... rules never match"; it warns at every launch).
func TestShippedManifestDeniesTheConfigFile(t *testing.T) {
	m, err := Load(repoPath(t, "roles.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range m.roleOrder {
		r, err := m.LaunchRules(role)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Edit(~/.config/agentbus/config.toml)",
			"Read(~/.config/agentbus/notify.toml)", "Edit(~/.config/agentbus/notify.toml)",
			"Bash(agentbus notifications test*)", "Bash(agentbus notifications add*)", "Bash(agentbus notifications remove*)",
			"Bash(agentbus notifications rename*)", "Bash(agentbus notifications setup*)", "Bash(agentbus notifications show-topic*)"} {
			if !slices.Contains(r.Deny, want) {
				t.Errorf("%s does not deny %s", role, want)
			}
		}
		for _, rule := range slices.Concat(r.Allow, r.Ask, r.Deny) {
			if neverMatches(rule) {
				t.Errorf("%s ships %s, a rule Claude Code never matches (use Edit)", role, rule)
			}
		}
		// No wildcard before the words (foureyes): a report or reply that
		// mentions the feature stays allowed; the command refuses an agent
		// itself (runNotifications).
		for _, d := range r.Deny {
			if strings.Contains(d, "* notifications") {
				t.Errorf("%s denies prose: %s", role, d)
			}
		}
	}
}

// ConfigDenies adds the path config.Path resolves when XDG_CONFIG_HOME moves
// it, in Claude Code's absolute form; nothing when it is the shipped one,
// and a note (nothing added) when it cannot be resolved.
func TestConfigDenies(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, c := range []struct {
		name string
		env  map[string]string
		want []string
		note bool
	}{
		{"default", map[string]string{"HOME": "/home/u"}, nil, false},
		{"xdg", map[string]string{"HOME": "/home/u", "XDG_CONFIG_HOME": "/x"},
			[]string{"Edit(//x/agentbus/config.toml)", "Read(//x/agentbus/notify.toml)", "Edit(//x/agentbus/notify.toml)"}, false},
		{"xdg = the default", map[string]string{"HOME": "/home/u", "XDG_CONFIG_HOME": "/home/u/.config"}, nil, false},
		{"relative xdg", map[string]string{"HOME": "/home/u", "XDG_CONFIG_HOME": "rel"}, nil, true},
		{"no home", map[string]string{}, nil, true},
	} {
		got, note := ConfigDenies(env(c.env))
		if !slices.Equal(got, c.want) || (note != "") != c.note {
			t.Errorf("%s: %v %q, want %v note=%v", c.name, got, note, c.want, c.note)
		}
		if note != "" && !strings.Contains(note, "config.toml") {
			t.Errorf("%s: note %q", c.name, note)
		}
	}
}

// A Claude Code launch whose XDG_CONFIG_HOME moves config.toml denies Edit
// of that path too (which covers Write); a relative one adds nothing and says so.
func TestLaunchDeniesTheConfigFileWhereItIs(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("claude-code", "", "", "")
	if _, _, err := f.launch(m, "coder", "XDG_CONFIG_HOME=/x"); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(f.execed, " ")
	for _, want := range []string{"Edit(//x/agentbus/config.toml)", "Read(//x/agentbus/notify.toml)"} {
		if !strings.Contains(args, want) {
			t.Errorf("launch lacks %s: %s", want, args)
		}
	}
	if strings.Contains(args, "Write(") {
		t.Errorf("launch carries a Write(path) rule Claude Code never matches: %s", args)
	}
	_, errw, err := f.launch(m, "coder", "XDG_CONFIG_HOME=rel")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(f.execed, " "), "agentbus/config.toml") || !strings.Contains(errw, "XDG_CONFIG_HOME is not absolute") {
		t.Fatalf("relative: %q %q", f.execed, errw)
	}
}

// neverMatches is a path rule for a tool Claude Code never checks a path
// against: Write, NotebookEdit, Glob or legacy MultiEdit with a parenthesis
// (its permissions docs; the bare tool name still matches every use).
func neverMatches(rule string) bool {
	for _, tool := range []string{"Write(", "NotebookEdit(", "Glob(", "MultiEdit("} {
		if strings.HasPrefix(rule, tool) {
			return true
		}
	}
	return false
}
