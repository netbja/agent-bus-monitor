package main

import (
	"strings"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/internal/finedraw"
)

// How fine busmon draws (tui-rendering §3, spec
// docs/superpowers/specs/2026-11-25-busmon-fine-rendering-design.md):
// display.rendering, with display.graphs (braille-sparklines) mapped onto it
// for one release, resolved once by resolveRendering; finedraw draws by the
// Caps of that mode and the theme's colour depth.
const (
	graphsAuto    = "auto"
	graphsBlock   = finedraw.StyleBlock
	graphsBraille = finedraw.StyleBraille
	graphsASCII   = finedraw.StyleASCII
)

// renderMode is the mode the frames draw in: set from the settings at
// start (newShell) and when display.rendering or display.graphs changes.
var renderMode = finedraw.ModeFine

// drawCaps is what a frame may draw: the mode and the theme's depth.
func drawCaps() finedraw.Caps { return finedraw.Caps{Mode: renderMode, Depth: th.Depth()} }

// localeUTF8: the locale's character set is UTF-8 (LC_ALL, LC_CTYPE, LANG,
// in that order, through config.Locale). TERM=linux does not matter: its
// fonts have the blocks.
func localeUTF8(getenv func(string) string) bool {
	l := strings.ToLower(config.Locale(getenv, "LC_CTYPE"))
	return strings.Contains(l, "utf-8") || strings.Contains(l, "utf8")
}

// resolveRendering is the mode and what decided it (finedraw.Resolve): a
// key counts as set when anything but its default gives it.
func resolveRendering(r config.Resolver, getenv func(string) string) (finedraw.Mode, string) {
	setting := func(name string) finedraw.Setting {
		v := r.Get(name)
		s, _ := v.Value.(string)
		return finedraw.Setting{Value: s, Set: !v.Invalid && v.Source != "default"}
	}
	return finedraw.Resolve(setting("display.rendering"), setting("display.graphs"), localeUTF8(getenv))
}

// renderingNote is the settings page's line under Rendering and Sparklines:
// how busmon draws now and what decided it.
func renderingNote(s busmonSettings) string {
	by := s.RenderingBy
	if by == "" {
		by = finedraw.ByAuto
	}
	return "drawn " + s.Rendering.String() + ", decided by " + by
}
