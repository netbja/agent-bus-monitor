package config

import "testing"

// start.team (agentbus-start): the default team for new projects, the
// --roles syntax: role or role:profile, comma-separated, each a name, no role
// twice. No env (a person's choice kept in a file); roles.toml is checked by
// agentbus start at use, since config imports no rolecfg.
func TestStartTeam(t *testing.T) {
	k, ok := Lookup("start.team")
	if !ok || k.Env != "" || k.Kind != String || k.Default != "" || k.Legacy || k.Group() != GroupIdentity {
		t.Fatalf("row %+v, group %q", k, k.Group())
	}
	for v, ok := range map[any]string{
		"":                        "",
		"master":                  "",
		"master,coder:review":     "",
		" master , foureyes:alt ": "",
		"master,master":           "master twice",
		"Master":                  "want role or role:profile, comma-separated",
		"master,a:b:c":            "want role or role:profile, comma-separated",
		"master,,coder":           "want role or role:profile, comma-separated",
		true:                      "want role or role:profile, comma-separated",
	} {
		got, why := k.file(v)
		if why != ok || (ok == "" && got != v) {
			t.Errorf("file %#v = %#v %q, want %q", v, got, why, ok)
		}
	}
	p := writeFile(t, "config.toml", "[start]\nteam = \"master,coder\"\n")
	if got := (Resolver{Getenv: env(nil), Files: []*File{Load(p, "config.toml")}}).Get("start.team"); got.Value != "master,coder" || got.Source != "config.toml" {
		t.Errorf("file: %+v", got)
	}
}
