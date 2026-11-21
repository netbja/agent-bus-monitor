package config

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
