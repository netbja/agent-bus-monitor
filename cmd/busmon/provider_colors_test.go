package main

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/quota"
)

// Every provider the quota readers know has its colour; an unknown provider
// keeps today's colour; the 16-colour palette takes the nearest ANSI colour,
// the no-colour palette none.
func TestEveryQuotaProviderHasItsColour(t *testing.T) {
	all := quota.Builtins(func(string) (string, error) { return "/bin/x", nil }) // every client found
	if len(all) < 5 {
		t.Fatalf("only %d built-in providers: %v", len(all), all)
	}
	for p := range all {
		if providerRole(p, RoleMuted) == RoleMuted {
			t.Errorf("provider %q has no colour", p)
		}
	}
	for _, p := range []string{"deepseek", "xiaomi"} {
		if providerRole(p, RoleMuted) == RoleMuted {
			t.Errorf("provider %q has no colour", p)
		}
	}
	if providerRole("someone-new", RoleMuted) != RoleMuted {
		t.Error("an unknown provider lost its fallback colour")
	}
	// The 16-colour palette (both reviews of 0b12f23): a hand-picked column,
	// no two providers alike, none an alert role's colour (attention olive,
	// error red, maroon) nor the text roles' own gray, silver or teal, and
	// openai in the terminal's own foreground, readable on dark and light.
	withPalette(t, "dark", false, 256)
	seen := map[string]string{}
	forbidden := map[string]bool{"red": true, "maroon": true, "olive": true, "gray": true, "silver": true, "teal": true}
	for _, role := range []Role{RoleAttention, RoleError, RoleMuted, RoleDim, RoleAccent, RoleLine} {
		forbidden[th.tags[role]] = true
	}
	for _, h := range providerHues {
		c := th.tags[h.role]
		if c == "" {
			t.Errorf("%s has no 16-colour value", h.provider)
		}
		if other, dup := seen[c]; dup {
			t.Errorf("%s and %s share the 16-colour %q", h.provider, other, c)
		}
		seen[c] = h.provider
		if forbidden[c] {
			t.Errorf("%s uses %q, a colour busmon gives a role of its own", h.provider, c)
		}
	}
	if th.colors[RoleProvOpenAI] != tcell.ColorDefault {
		t.Errorf("openai on 16 colours = %q, want the terminal's own foreground", th.tags[RoleProvOpenAI])
	}
	withPalette(t, "light", false, 256)
	if th.colors[RoleProvOpenAI] != tcell.ColorDefault {
		t.Errorf("openai on light 16 colours = %q: unreadable on white", th.tags[RoleProvOpenAI])
	}
	withPalette(t, "dark", false, 1<<24)
	if r, g, b := th.colors[RoleProvAnthropic].RGB(); r != 0xCC || g != 0x78 || b != 0x5C {
		t.Errorf("anthropic on dark = %d,%d,%d", r, g, b)
	}
	withPalette(t, "light", false, 1<<24)
	if r, g, b := th.colors[RoleProvOpenAI].RGB(); r != 0x20 || g != 0x21 || b != 0x23 {
		t.Errorf("openai on light = %d,%d,%d", r, g, b)
	}
	withPalette(t, "dark", true, 1<<24)
	for _, h := range providerHues {
		if th.Style(h.role) != th.Style(RoleText) {
			t.Errorf("%s coloured with no colour", h.provider)
		}
	}
}

// The header gauge's label carries the provider's colour; the fill keeps its
// warning roles.
func TestQuotaGaugeLabelInProviderColour(t *testing.T) {
	l := budgetSeg("anthropic", bus.BudgetSnapshot{SessionPct: 97, WeeklyPct: 10}, TierWide, barView{})
	if l[0].Role != RoleProvAnthropic {
		t.Errorf("label role %v", l[0].Role)
	}
	warned := false
	for _, s := range l {
		if s.Role == RoleError {
			warned = true
		}
	}
	if !warned {
		t.Error("the 95% warning colour left the gauge")
	}
}
