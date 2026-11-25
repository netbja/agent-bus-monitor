package main

import (
	"strings"

	"github.com/netbja/agent-bus-monitor/config"
	"github.com/netbja/agent-bus-monitor/internal/finedraw"
)

// The sparklines' styles (braille-sparklines, spec
// docs/superpowers/specs/2026-11-18-braille-sparklines-design.md): the
// display.graphs setting, auto resolved by graphsStyle; finedraw draws them.
// Gauges and bars keep their blocks whatever the style.
const (
	graphsAuto    = "auto"
	graphsBlock   = finedraw.StyleBlock
	graphsBraille = finedraw.StyleBraille
	graphsASCII   = finedraw.StyleASCII
)

// graphsStyle resolves display.graphs: an explicit block, braille or ascii
// wins; auto (and an unknown or empty value) is ascii when the locale is not
// UTF-8, else block. auto never gives braille (§2).
func graphsStyle(setting string, getenv func(string) string) string {
	switch setting {
	case graphsBlock, graphsBraille, graphsASCII:
		return setting
	}
	l := strings.ToLower(config.Locale(getenv, "LC_CTYPE"))
	if !strings.Contains(l, "utf-8") && !strings.Contains(l, "utf8") {
		return graphsASCII
	}
	return graphsBlock // TERM=linux included: its fonts have the blocks
}
