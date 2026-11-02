package main

import (
	"testing"

	"github.com/netbja/agent-bus-monitor/quota"
	"github.com/netbja/agent-bus-monitor/rolecfg"
)

// busmon-icons §5: each icon is one Private Use rune, one cell by every width
// method busmon checks, and unique; the UI glyph set keeps its own count.
func TestIconsAreSingleCell(t *testing.T) {
	if len(IconGlyphs) == 0 {
		t.Fatal("no icon glyphs")
	}
	seen := map[string]bool{}
	for _, g := range IconGlyphs {
		r := []rune(g)
		if len(r) != 1 {
			t.Errorf("icon %q is %d runes", g, len(r))
			continue
		}
		pua := (r[0] >= 0xE000 && r[0] <= 0xF8FF) || (r[0] >= 0xF0000 && r[0] <= 0xFFFFD)
		if !pua || Width(g) != 1 || !widthsAgree(g) || seen[g] {
			t.Errorf("invalid icon U+%04X", r[0])
		}
		seen[g] = true
	}
}

// busmon-icons §3.1 row by row: agent-font keys on the client, then the
// provider; nerd on the provider only; off draws nothing; the person never.
func TestIconTables(t *testing.T) {
	cases := []struct{ client, provider, font, nerd string }{
		{"claude-code", "anthropic", "", "\U000f0b08"},
		{"codex", "openai", "", "\U000f0b16"},
		{"kimi-cli", "moonshot", "", "\U000f0b12"},
		{"grok", "xai", "", "\U000f0b1f"},
		{"agy", "google", "", "\U000f0b0e"},
		{"", "google", "", "\U000f0b0e"},
		{"", "deepseek", "", "\U000f0b0b"},
		{"", "xiaomi", "", "\U000f0b14"},
		{"", "local", "", "\U000f0b13"},
		{"codex", "local", "", "\U000f0b13"}, // the mismatched row: the tool's logo, the provider's box
		{"", "", "", ""},
		{"someone", "someone", "", ""},
	}
	for _, c := range cases {
		if got := agentIcon(iconsFont, c.client, c.provider); got != c.font {
			t.Errorf("agent-font %s/%s = %q, want %q", c.client, c.provider, got, c.font)
		}
		if got := agentIcon(iconsNerd, c.client, c.provider); got != c.nerd {
			t.Errorf("nerd %s/%s = %q, want %q", c.client, c.provider, got, c.nerd)
		}
		if got := agentIcon(iconsOff, c.client, c.provider); got != "" {
			t.Errorf("off %s/%s = %q", c.client, c.provider, got)
		}
	}
	// An account row is keyed by provider in both modes.
	if providerIcon(iconsFont, "anthropic") != "" || providerIcon(iconsNerd, "anthropic") != "\U000f0b08" || providerIcon(iconsOff, "anthropic") != "" {
		t.Error("providerIcon")
	}
	// Every client the launcher knows and every provider with a colour has an icon.
	for _, c := range rolecfg.Clients {
		if agentIcon(iconsFont, c.Client, "") == "" {
			t.Errorf("client %s has no agent-font icon", c.Client)
		}
	}
	for _, h := range providerHues {
		if providerIcon(iconsNerd, h.provider) == "" {
			t.Errorf("provider %s has no nerd icon", h.provider)
		}
	}
	for p := range quota.Builtins(func(string) (string, error) { return "/bin/x", nil }) {
		if providerIcon(iconsNerd, p) == "" {
			t.Errorf("built-in provider %s has no nerd icon", p)
		}
	}
	// Every glyph a table returns is in IconGlyphs.
	in := map[string]bool{}
	for _, g := range IconGlyphs {
		in[g] = true
	}
	for _, c := range cases {
		for _, g := range []string{c.font, c.nerd} {
			if g != "" && !in[g] {
				t.Errorf("icon %q not in IconGlyphs", g)
			}
		}
	}
}
