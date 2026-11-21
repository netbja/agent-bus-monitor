package config

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"
)

// language-picker: the ISO 639-1 table carries each code's English and
// native names, so a list can show "fr  Français (French)"; the codes are
// exactly the ones person.language took before (no code added or lost).
func TestLanguageTable(t *testing.T) {
	before := strings.Fields(`
aa ab ae af ak am an ar as av ay az ba be bg bi bm bn bo br bs ca ce ch co cr cs cu cv cy
da de dv dz ee el en eo es et eu fa ff fi fj fo fr fy ga gd gl gn gu gv ha he hi ho hr ht
hu hy hz ia id ie ig ii ik io is it iu ja jv ka kg ki kj kk kl km kn ko kr ks ku kv kw ky
la lb lg li ln lo lt lu lv mg mh mi mk ml mn mr ms mt my na nb nd ne ng nl nn no nr nv ny
oc oj om or os pa pi pl ps pt qu rm rn ro ru rw sa sc sd se sg si sk sl sm sn so sq sr ss
st su sv sw ta te tg th ti tk tl tn to tr ts tt tw ty ug uk ur uz ve vi vo wa wo xh yi yo
za zh zu`)
	var codes []string
	for _, l := range Languages() {
		codes = append(codes, l.Code)
		if l.English == "" || l.Native == "" {
			t.Errorf("%s: a name is missing: %+v", l.Code, l)
		}
		if strings.ContainsFunc(l.English+l.Native, unicode.IsControl) {
			t.Errorf("%s: a control character", l.Code)
		}
	}
	if !slices.Equal(codes, before) {
		t.Fatalf("codes differ from the ISO 639-1 list:\n%v", codes)
	}
	fr, ok := LanguageOf("fr")
	if !ok || fr.English != "French" || fr.Native != "français" {
		t.Fatalf("fr: %+v %v", fr, ok)
	}
	if _, ok := LanguageOf("xx"); ok {
		t.Fatal("xx is a language")
	}
}

// A key with a fixed list offers it (Options): person.language lists every
// language, code first, its label "Français (French)"; a key without one
// offers nothing.
func TestKeyOptions(t *testing.T) {
	k, _ := Lookup("person.language")
	opts := k.Choices()
	if len(opts) != len(Languages()) {
		t.Fatalf("%d options, want %d", len(opts), len(Languages()))
	}
	for _, o := range opts {
		if o.Value == "fr" && o.Label != "Français (French)" {
			t.Errorf("fr's label %q", o.Label)
		}
		if o.Value == "en" && o.Label != "English" {
			t.Errorf("en's label %q (the native name and the English one are the same)", o.Label)
		}
		if _, why := k.file(o.Value); why != "" {
			t.Errorf("option %q refused by its own key: %s", o.Value, why)
		}
	}
	other, _ := Lookup("display.follow")
	if other.Choices() != nil {
		t.Error("display.follow offers a list")
	}
}

// agentbus config set (config.Set) still refuses a code not in the table:
// the list is the way in busmon, never the only check.
func TestSetRefusesAnUnknownLanguage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	var refused *RefusedError
	if err := Set(path, "person.language", "frnvv"); !errors.As(err, &refused) {
		t.Fatalf("frnvv: %v", err)
	}
	if err := Set(path, "person.language", "fr"); err != nil {
		t.Fatal(err)
	}
}
