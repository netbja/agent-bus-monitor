package config

import "testing"

// person.language (language-setting spec §2, the person's answer to §7 Q2): the
// language the person reads, any ISO 639-1 code, en by default; a value that is
// not one is refused by the env rule and the file check (fail closed), so it
// resolves Invalid with en as its value and agentbus config says so.
func TestPersonLanguageKey(t *testing.T) {
	k, ok := Lookup("person.language")
	if !ok {
		t.Fatal("person.language not registered")
	}
	if k.Kind != String || k.Default != "en" || k.Env != "AGENT_BUS_PERSON_LANGUAGE" ||
		k.ReadBy != "master's role skill (agentbus config)" || k.Invalid != "master writes English" ||
		!k.FailClosed || k.Group() != GroupIdentity {
		t.Fatalf("%+v group %q", k, k.Group())
	}
	for _, good := range []string{"en", "fr", "de", "ja", "zh", "yo"} {
		if v, res := k.env(good); res != envUse || v != good {
			t.Errorf("env %q: %v %v", good, v, res)
		}
		if v, why := k.file(good); why != "" || v != good {
			t.Errorf("file %q: %v %q", good, v, why)
		}
	}
	for _, bad := range []string{"xx", "qq", "FR", "fra", "f", "fr-FR", "fr_FR", " fr", "français"} {
		if _, res := k.env(bad); res != envInvalid {
			t.Errorf("env %q: %v, want refused", bad, res)
		}
		if _, why := k.file(bad); why == "" {
			t.Errorf("file %q taken", bad)
		}
	}
	if _, why := k.file(3); why == "" {
		t.Error("file 3 taken")
	}
}

// An invalid value falls back to en and says so: Invalid, value en, from the
// env and from config.toml alike.
func TestPersonLanguageFallsBackToEnglish(t *testing.T) {
	env := Resolver{Getenv: func(n string) string {
		if n == "AGENT_BUS_PERSON_LANGUAGE" {
			return "xx"
		}
		return ""
	}}.Get("person.language")
	if !env.Invalid || env.Value != "en" {
		t.Fatalf("env xx: %+v", env)
	}
	f := Load(writeFile(t, "config.toml", "[person]\nlanguage = \"zz\"\n"), "config.toml")
	got := Resolver{Files: []*File{f}}.Get("person.language")
	if !got.Invalid || got.Value != "en" || got.Line != 2 {
		t.Fatalf("file zz: %+v", got)
	}
	ok := Resolver{Files: []*File{Load(writeFile(t, "config.toml", "[person]\nlanguage = \"fr\"\n"), "config.toml")}}.Get("person.language")
	if ok.Invalid || ok.Value != "fr" {
		t.Fatalf("file fr: %+v", ok)
	}
}
