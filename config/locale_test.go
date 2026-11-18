package config

import "testing"

// Locale is the locale a category reads, the first non-empty of LC_ALL, the
// category's variable and LANG: one helper for busmon's sparklines
// (LC_CTYPE, braille-sparklines §4) and the language setting (LC_MESSAGES).
func TestLocale(t *testing.T) {
	for _, c := range []struct {
		env  map[string]string
		cat  string
		want string
	}{
		{nil, "LC_CTYPE", ""},
		{map[string]string{"LANG": "a"}, "LC_CTYPE", "a"},
		{map[string]string{"LANG": "a", "LC_CTYPE": "b"}, "LC_CTYPE", "b"},
		{map[string]string{"LANG": "a", "LC_CTYPE": "b", "LC_ALL": "c"}, "LC_CTYPE", "c"},
		{map[string]string{"LANG": "a", "LC_CTYPE": "b"}, "LC_MESSAGES", "a"},
		{map[string]string{"LANG": "a", "LC_MESSAGES": "m"}, "LC_MESSAGES", "m"},
		{map[string]string{"LC_ALL": "c", "LC_MESSAGES": "m"}, "LC_MESSAGES", "c"},
	} {
		if got := Locale(func(k string) string { return c.env[k] }, c.cat); got != c.want {
			t.Errorf("Locale(%v, %s) = %q, want %q", c.env, c.cat, got, c.want)
		}
	}
}
