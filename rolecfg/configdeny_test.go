package rolecfg

import (
	"slices"
	"strings"
	"testing"
)

// backup.after_command is the person's alone (backup spec): the shipped
// manifest denies every role Edit and Write of ~/.config/agentbus/config.toml.
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
		for _, want := range []string{"Edit(~/.config/agentbus/config.toml)", "Write(~/.config/agentbus/config.toml)"} {
			if !slices.Contains(r.Deny, want) {
				t.Errorf("%s does not deny %s", role, want)
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
			[]string{"Edit(//x/agentbus/config.toml)", "Write(//x/agentbus/config.toml)"}, false},
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
// and Write of that path too; a relative one adds nothing and says so.
func TestLaunchDeniesTheConfigFileWhereItIs(t *testing.T) {
	f := newLaunchFixture(t)
	m := f.config("claude-code", "", "", "")
	if _, _, err := f.launch(m, "coder", "XDG_CONFIG_HOME=/x"); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(f.execed, " ")
	for _, want := range []string{"Edit(//x/agentbus/config.toml)", "Write(//x/agentbus/config.toml)"} {
		if !strings.Contains(args, want) {
			t.Errorf("launch lacks %s: %s", want, args)
		}
	}
	_, errw, err := f.launch(m, "coder", "XDG_CONFIG_HOME=rel")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(f.execed, " "), "agentbus/config.toml") || !strings.Contains(errw, "XDG_CONFIG_HOME is not absolute") {
		t.Fatalf("relative: %q %q", f.execed, errw)
	}
}
