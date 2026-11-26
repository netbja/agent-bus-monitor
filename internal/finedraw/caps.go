package finedraw

import "math"

// Mode is the rendering level (spec §3): display.rendering resolved.
type Mode int

const (
	ModeFine  Mode = iota // eighths and gradients on a track, braille sparklines
	ModeBasic             // whole cells (▇ ░), block sparklines, role colours
	ModeASCII             // a locale without UTF-8: # and -, ascii sparklines
)

func (m Mode) String() string {
	switch m {
	case ModeFine:
		return "fine"
	case ModeBasic:
		return "basic"
	}
	return "ascii"
}

// Depth is the terminal's colour depth.
type Depth int

const (
	DepthNone Depth = iota // NO_COLOR, or no colour at all
	Depth16
	Depth256
	DepthTrue
)

// DepthOf is the depth of a terminal of colors colours (colorprofile's
// reading), none under NO_COLOR.
func DepthOf(colors int, noColor bool) Depth {
	switch {
	case noColor || colors <= 0:
		return DepthNone
	case colors >= 1<<24:
		return DepthTrue
	case colors >= 256:
		return Depth256
	}
	return Depth16
}

// Caps is what the terminal and the settings allow: resolved at start and
// on a settings or theme change.
type Caps struct {
	Mode  Mode
	Depth Depth
}

// BarMode is how bars and gauges draw (§3's table): eighths on a track only
// where the track has a colour to show (256 colours and up), whole cells
// otherwise; ascii stays ascii.
func (c Caps) BarMode() Mode {
	if c.Mode == ModeFine && c.Depth < Depth256 {
		return ModeBasic
	}
	return c.Mode
}

// SparkStyle is the sparklines' glyphs: braille in fine, blocks in basic,
// ascii in ascii. Glyphs need no colour, so the depth does not matter.
func (c Caps) SparkStyle() string {
	switch c.Mode {
	case ModeFine:
		return StyleBraille
	case ModeBasic:
		return StyleBlock
	}
	return StyleASCII
}

// Setting is a key's value and whether it was set (any source but its
// default).
type Setting struct {
	Value string
	Set   bool
}

// What decided the mode (the settings page shows it).
const (
	ByLocale    = "the locale (not UTF-8)"
	ByRendering = "display.rendering"
	ByGraphs    = "display.graphs (mapped)"
	ByAuto      = "auto"
)

// Resolve is the mode (§3, the person's Q1 and Q4): a locale without UTF-8
// forces ascii whatever is set; else an explicitly set display.rendering
// wins (auto, fine or basic); else an explicitly set display.graphs, mapped
// (block basic, braille fine, ascii ascii; its auto is no choice); else
// auto, which is fine on a UTF-8 locale.
func Resolve(rendering, graphs Setting, utf8 bool) (Mode, string) {
	if !utf8 {
		return ModeASCII, ByLocale
	}
	if rendering.Set {
		switch rendering.Value {
		case "auto", "fine":
			return ModeFine, ByRendering
		case "basic":
			return ModeBasic, ByRendering
		}
	}
	if graphs.Set {
		switch graphs.Value {
		case StyleBraille:
			return ModeFine, ByGraphs
		case StyleBlock:
			return ModeBasic, ByGraphs
		case StyleASCII:
			return ModeASCII, ByGraphs
		}
	}
	return ModeFine, ByAuto
}

// RGB is a colour of a ramp.
type RGB struct{ R, G, B uint8 }

// Ramp is a gradient: its stops spread evenly from 0 to 100 %, linear
// between them in RGB.
type Ramp []RGB

// At is the ramp's colour at pct (clamped to 0-100).
func (r Ramp) At(pct float64) RGB {
	if len(r) == 0 {
		return RGB{}
	}
	if len(r) == 1 {
		return r[0]
	}
	x := math.Max(0, math.Min(100, pct)) / 100 * float64(len(r)-1)
	i := min(int(x), len(r)-2)
	f := x - float64(i)
	mix := func(a, b uint8) uint8 { return uint8(math.Round(float64(a) + (float64(b)-float64(a))*f)) }
	a, b := r[i], r[i+1]
	return RGB{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B)}
}

// Stop is one colour of Stops, at pct.
type Stop struct {
	Pct float64
	C   RGB
}

// Stops is a gradient whose colours sit at given percents (ascending):
// linear between two stops, flat before the first and past the last (the
// context ramp: amber at the alert threshold, red at the urgent one).
type Stops []Stop

// At is the colour at pct; at two stops in one place, the later one.
func (s Stops) At(pct float64) RGB {
	if len(s) == 0 {
		return RGB{}
	}
	if pct < s[0].Pct {
		return s[0].C
	}
	for i := len(s) - 1; i > 0; i-- {
		a, b := s[i-1], s[i]
		if pct >= b.Pct {
			return b.C
		}
		if pct >= a.Pct {
			f := (pct - a.Pct) / (b.Pct - a.Pct)
			mix := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*f)) }
			return RGB{mix(a.C.R, b.C.R), mix(a.C.G, b.C.G), mix(a.C.B, b.C.B)}
		}
	}
	return s[0].C
}

// Gradient is a ramp of either form: a colour for each percent.
type Gradient interface{ At(pct float64) RGB }

// Shade is a ramp cell's colour at a depth: the ramp's RGB from 256
// colours up in fine (the terminal library downsamples it to 256), else
// the band among the thresholds (0 below the first; the caller's role
// colours, as busmon's three gauge roles today), with no colour at all
// without colour.
type Shade struct {
	RGB    RGB
	UseRGB bool
	Band   int
	Colour bool
}

// Shade is pct on ramp r with the 16-colour thresholds bands (ascending).
func (c Caps) Shade(r Gradient, pct float64, bands []float64) Shade {
	s := Shade{Colour: c.Depth > DepthNone}
	for _, b := range bands {
		if pct >= b {
			s.Band++
		}
	}
	if c.Mode == ModeFine && c.Depth >= Depth256 {
		s.RGB, s.UseRGB = r.At(pct), true
	}
	return s
}
