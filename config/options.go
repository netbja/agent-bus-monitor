package config

import "strings"

// Fixed lists (language-picker): a key whose values are a known set offers
// them through Options, for a page to show as a list rather than free text.

// Option is one value of a key's fixed list: the value written, and how a
// list shows it next to the value.
type Option struct {
	Value string
	Label string
}

// Choices is k's fixed list of values, nil when it has none.
func (k *Key) Choices() []Option {
	if k.Options == nil {
		return nil
	}
	return k.Options()
}

// Search is what a list searches o by: its label, and its label without
// accents, lower case (busmon's list: francais finds Français).
func (o Option) Search() string { return o.Label + " " + FoldAccents(o.Label) }

// accentFold maps the accented Latin letters to their base letter.
var accentFold = strings.NewReplacer(
	"à", "a", "á", "a", "â", "a", "ã", "a", "ä", "a", "å", "a", "ā", "a", "ă", "a", "ą", "a",
	"ç", "c", "č", "c", "ć", "c", "ď", "d", "ḓ", "d",
	"è", "e", "é", "e", "ê", "e", "ë", "e", "ē", "e", "ě", "e", "ę", "e", "ẽ", "e",
	"ì", "i", "í", "i", "î", "i", "ï", "i", "ĩ", "i", "ī", "i",
	"ñ", "n", "ň", "n", "ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "o", "ø", "o", "ō", "o", "ő", "o",
	"ř", "r", "š", "s", "ș", "s", "ş", "s", "ť", "t", "ț", "t",
	"ù", "u", "ú", "u", "û", "u", "ü", "u", "ũ", "u", "ū", "u", "ů", "u", "ű", "u",
	"ý", "y", "ÿ", "y", "ž", "z", "ŵ", "w", "ļ", "l", "ł", "l", "ğ", "g", "ı", "i", "ə", "e")

// FoldAccents is s lower-cased, its accented Latin letters plain.
func FoldAccents(s string) string { return accentFold.Replace(strings.ToLower(s)) }
