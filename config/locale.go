package config

// Locale is the locale a category reads under POSIX precedence: the first
// non-empty of LC_ALL, category's own variable (LC_CTYPE, LC_MESSAGES) and
// LANG; "" when none is set. busmon's sparklines read LC_CTYPE through it
// (braille-sparklines §4) and the language setting reads LC_MESSAGES, so the
// precedence cannot drift between them. Exported here, not in busmon, so
// agentbus and an i18n package can share it.
func Locale(getenv func(string) string, category string) string {
	for _, k := range []string{"LC_ALL", category, "LANG"} {
		if v := getenv(k); v != "" {
			return v
		}
	}
	return ""
}
