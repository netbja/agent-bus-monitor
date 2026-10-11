package main

import (
	"context"
	"fmt"
	"html"
	"os"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
)

// TestCaptureScreen renders one busmon screen of a live project on the test
// broker to an HTML page, for the README screenshot (rasterise it with a
// headless browser). It never runs by default:
//
//	AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392 BUSMON_CAPTURE=out.html \
//	BUSMON_CAPTURE_PROJECT=readme go test ./cmd/busmon -run TestCaptureScreen
//
// BUSMON_CAPTURE_THEME (dark|light, default dark) picks the palette and the
// page colours that stand in for the terminal's own background.
func TestCaptureScreen(t *testing.T) {
	out := os.Getenv("BUSMON_CAPTURE")
	if out == "" {
		t.Skip("set BUSMON_CAPTURE=<file.html> to render a screenshot")
	}
	testredis.Configure(t)
	project := os.Getenv("BUSMON_CAPTURE_PROJECT")
	if project == "" {
		project = "readme"
	}
	theme := os.Getenv("BUSMON_CAPTURE_THEME")
	if theme == "" {
		theme = "dark"
	}
	saved := th
	defer func() { th = saved }()
	var err error
	if th, err = ResolveTheme(theme, "", "", false, 1<<24); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	rc, err := bus.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(rc, project)
	if err != nil {
		t.Fatal(err)
	}
	st := &shared{agents: map[string]*agentState{}, budgets: map[string]bus.BudgetSnapshot{}, board: map[string]bus.BoardEntry{}}
	h := newHarness(t, st)
	recent, _, err := b.Recent(ctx, streamKinds, 40)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range recent {
		h.u.ingest(e)
		h.u.appendEvent(e)
	}
	pollOnce(ctx, b, st, 1)
	h.draw(160, 42)
	h.draw(160, 42) // the first draw settles the layout the second one fills

	fg, bg := "#C9D1D9", "#0D1117"
	if theme == "light" {
		fg, bg = "#1F2328", "#FFFFFF"
	}
	if err := os.WriteFile(out, []byte(screenHTML(h.sim, fg, bg)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// screenHTML is the simulated screen as a page, one fixed-size box per cell so
// the grid holds whatever font the browser picks. Frame lines and bar blocks
// are drawn in CSS, as a terminal draws them, instead of trusting a font's
// box-drawing glyphs to fill the cell; the terminal default colours become
// fg/bg.
func screenHTML(sim tcell.SimulationScreen, fg, bg string) string {
	cells, w, hh := sim.GetContents()
	hex := func(c tcell.Color, def string) string {
		if c == tcell.ColorDefault || !c.Valid() {
			return def
		}
		return fmt.Sprintf("#%06X", c.Hex())
	}
	drawn := map[string]string{
		glyphVertical: "v", glyphThumb: "t", glyphHorizontal: "h",
		glyphTopLeft: "tl", glyphTopRight: "tr", glyphBottomLeft: "bl", glyphBottomRight: "br",
		GlyphBarFull: "full", GlyphBarEmpty: "shade", GlyphGauge: "gauge",
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, `<!doctype html><meta charset="utf-8"><style>
body{margin:0;background:transparent}
.win{display:inline-block;margin:24px;border-radius:10px;background:%[1]s;box-shadow:0 10px 30px rgba(0,0,0,.35);overflow:hidden}
.bar{height:28px;background:#21262D;display:flex;align-items:center;padding-left:12px;gap:8px}
.bar i{width:12px;height:12px;border-radius:50%%;display:block}
.scr{padding:12px 14px;color:%[2]s;font:14px Hack,"DejaVu Sans Mono",monospace}
.r{display:flex;height:18px}
.r b{font-weight:inherit;flex:0 0 8.5px;min-width:0;width:8.5px;height:18px;line-height:18px;text-align:center;white-space:pre;position:relative;overflow:visible}
.v:before,.t:before,.h:before{content:"";position:absolute;background:currentColor}
.v:before{left:calc(50%% - .7px);width:1.4px;top:0;bottom:0}
.t:before{left:calc(50%% - 1.5px);width:3px;top:0;bottom:0}
.h:before{top:calc(50%% - .7px);height:1.4px;left:0;right:0}
.full,.shade{background:linear-gradient(currentColor,currentColor) center/100%% 62%% no-repeat}
.shade{opacity:.35}
.gauge{background:linear-gradient(currentColor,currentColor) bottom/100%% 80%% no-repeat}
.tl:after,.tr:after,.bl:after,.br:after{content:"";position:absolute;width:calc(50%% + .7px);height:calc(50%% + .7px);border:0 solid currentColor}
.tl:after{right:0;bottom:0;border-top-width:1.4px;border-left-width:1.4px;border-top-left-radius:6px}
.tr:after{left:0;bottom:0;border-top-width:1.4px;border-right-width:1.4px;border-top-right-radius:6px}
.bl:after{right:0;top:0;border-bottom-width:1.4px;border-left-width:1.4px;border-bottom-left-radius:6px}
.br:after{left:0;top:0;border-bottom-width:1.4px;border-right-width:1.4px;border-bottom-right-radius:6px}
</style><div class="win"><div class="bar"><i style="background:#FF5F57"></i><i style="background:#FEBC2E"></i><i style="background:#28C840"></i></div><div class="scr">`, bg, fg)
	for y := 0; y < hh; y++ {
		sb.WriteString(`<div class="r">`)
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			f, b, a := c.Style.Decompose()
			cf, cb := hex(f, fg), hex(b, bg)
			if a&tcell.AttrReverse != 0 {
				cf, cb = cb, cf
			}
			style := "color:" + cf
			if cb != bg {
				style += ";background-color:" + cb
			}
			if a&tcell.AttrBold != 0 {
				style += ";font-weight:bold"
			}
			s := string(c.Runes)
			if cls, ok := drawn[s]; ok {
				fmt.Fprintf(&sb, `<b class="%s" style="%s"></b>`, cls, style)
				continue
			}
			if s == "" {
				s = " "
			}
			fmt.Fprintf(&sb, `<b style="%s">%s</b>`, style, html.EscapeString(s))
		}
		sb.WriteString("</div>")
	}
	sb.WriteString("</div></div>")
	return sb.String()
}
