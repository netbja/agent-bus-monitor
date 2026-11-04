package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/buildinfo"
	"github.com/netbja/agent-bus-monitor/bus"
)

// About (A, about-page; the v0.27 plan, item 6): the versions of agentbus,
// busmon, Redis, herdr, the plugin, the project and the update state (y
// copies them), then two agents on a bus line, a packet travelling from
// master to coder and back; r replays. The versions are read in a command.
// The packet moves only on the fade schedule (animated, fadeMsg), as every
// motion of busmon: with fade off, 16 colours or no colour the drawing is
// still and r says so. Its run starts at the first fade frame after the open
// (or after r), so its time comes from a message, never from a clock read.

// aboutPoint is a cell of the drawing: x a column, y a row (0 the agents'
// nodes, 1 their stems, 2 the bus).
type aboutPoint struct{ x, y int }

const (
	aboutMasterX = 6
	aboutCoderX  = 26
	aboutBusW    = 33
	aboutCell    = 60 * time.Millisecond // the packet's pace: a cell, about two per fade frame
)

// aboutRoute is the packet's every cell: down master's stem, along the bus
// to coder, up into coder; then the same way back.
func aboutRoute() []aboutPoint {
	out := []aboutPoint{{aboutMasterX, 0}, {aboutMasterX, 1}}
	for x := aboutMasterX; x <= aboutCoderX; x++ {
		out = append(out, aboutPoint{x, 2})
	}
	out = append(out, aboutPoint{aboutCoderX, 1}, aboutPoint{aboutCoderX, 0})
	n := len(out)
	for i := n - 2; i >= 0; i-- {
		out = append(out, out[i])
	}
	return out
}

// aboutVersions are the lines y copies, and the same facts as the rows the
// page aligns (about-design): the project, then this build, then this host.
type aboutVersions struct {
	Lines   []string
	Project string
	Build   []aboutRow // busmon, agentbus, protocol, go, updates
	Host    []aboutRow // redis, herdr, plugin
}

// aboutRow is a label, its value and an optional note after it (the
// agentbus beside busmon: same build or not; the update state's mark).
type aboutRow struct {
	Label, Value string
	Role         Role // the value's colour
	Note         string
	NoteRole     Role
}

// aboutState is the page's run: start is the first fade frame's time (zero:
// not started yet, or a replay waiting for its first frame).
type aboutState struct {
	start    time.Time
	versions aboutVersions
	loaded   bool
}

// step is the packet's cell index at now: 0 before the run, len(route) once
// it is over.
func (a aboutState) step(now time.Time) int {
	if a.start.IsZero() {
		return 0
	}
	return clamp(int(now.Sub(a.start)/aboutCell), 0, len(aboutRoute()))
}

type aboutVersionsMsg struct {
	serial int
	v      aboutVersions
}

// aboutSources is where About reads what it cannot know itself (a test
// replaces it).
type aboutSources struct {
	agentbus func() string // `agentbus version`'s line
	herdr    func() string // `herdr --version`'s line
	plugin   func() string // herdr-plugin.toml's version
	update   func() string // the update check's cached state
}

// openAbout opens the page, reads the versions and lets the fade schedule
// play the run.
func (m *Model) openAbout() tea.Cmd {
	m.push(page{kind: ovAbout, about: &aboutState{}})
	serial := m.rd.top().serial
	r, ctx, src, project := m.cfg.reader, m.cfg.ctx, m.cfg.about, m.cfg.project
	if !m.cfg.settings.UpdateCheck {
		src.update = func() string { return "update check off (updates.check)" }
	}
	if ctx == nil {
		ctx = context.Background()
	}
	or := func(f func() string, unknown string) string {
		if f == nil {
			return unknown
		}
		return f()
	}
	read := func() tea.Msg {
		redis := "?"
		if ri, ok := r.(redisInfoReader); ok {
			c, cancel := context.WithTimeout(ctx, 2*time.Second)
			if info, err := ri.RedisInfo(c); err == nil && info.Version != "" {
				redis = info.Version
			}
			cancel()
		}
		agentbus, herdr := or(src.agentbus, "agentbus ?"), or(src.herdr, "herdr ?")
		plugin, update := or(src.plugin, "?"), or(src.update, "update unknown")
		goLine := runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH
		return aboutVersionsMsg{serial: serial, v: aboutVersions{
			Lines: []string{
				"project " + project,
				"busmon " + buildinfo.Version,
				agentbus,
				fmt.Sprintf("protocol v%d", bus.ProtocolVersion),
				"redis " + redis,
				herdr,
				"plugin " + plugin,
				update,
				goLine,
			},
			Project: project,
			Build: []aboutRow{
				{Label: "busmon", Value: buildinfo.Version},
				agentbusRow(strings.TrimPrefix(agentbus, "agentbus version "), buildinfo.Version),
				{Label: "protocol", Value: fmt.Sprintf("v%d", bus.ProtocolVersion)},
				{Label: "go", Value: goLine},
				updateRow(update),
			},
			Host: []aboutRow{
				{Label: "redis", Value: redis},
				{Label: "herdr", Value: strings.TrimPrefix(herdr, "herdr ")},
				{Label: "plugin", Value: plugin},
			},
		}}
	}
	return tea.Batch(read, m.scheduleFade())
}

// aboutPage is the open About page with that serial.
func (m *Model) aboutPage(serial int) *page {
	for i := range m.rd.pages {
		if p := &m.rd.pages[i]; p.serial == serial && p.kind == ovAbout {
			return p
		}
	}
	return nil
}

func (m *Model) gotAboutVersions(msg aboutVersionsMsg) {
	if p := m.aboutPage(msg.serial); p != nil {
		p.about.versions, p.about.loaded, p.dirty = msg.v, true, true
		m.refresh()
	}
}

// aboutMotion: the packet may move (busmon's motion rule).
func (m *Model) aboutMotion() bool { return m.cfg.settings.Fade && th.Animated() }

// aboutPlaying: About is the top page and its run is not over (animated
// reads it, after the motion rule).
func (m *Model) aboutPlaying() bool {
	p := m.rd.top()
	return p != nil && p.kind == ovAbout && p.about.step(m.now) < len(aboutRoute())
}

// aboutFrame is a fade frame seen by About: the run starts on the first one.
func (m *Model) aboutFrame(at time.Time) {
	if p := m.rd.top(); p != nil && p.kind == ovAbout && m.aboutMotion() {
		if p.about.start.IsZero() {
			p.about.start = at
		}
		p.dirty = true
	}
}

// aboutReplay is r: a new run from the start, when motion is on.
func (m *Model) aboutReplay(p *page) tea.Cmd {
	if !m.aboutMotion() {
		m.ui.flash, m.ui.flashErr = "nothing to replay: the bus is still with fade off, 16 colours or no colour", true
		return nil
	}
	p.about.start, p.dirty = time.Time{}, true
	m.refresh()
	return m.scheduleFade()
}

// agentbusRow is the agentbus beside busmon: the same build or not (an
// unknown version says nothing).
func agentbusRow(v, busmon string) aboutRow {
	if v == "agentbus ?" {
		v = "?"
	}
	r := aboutRow{Label: "agentbus", Value: v}
	switch {
	case v == "?":
	case v == busmon:
		r.Note, r.NoteRole = GlyphDone+" same build", RoleOK
	default:
		r.Note, r.NoteRole = GlyphNotEqual+" busmon", RoleAttention
	}
	return r
}

// updateRow is the update check's cached state, marked: up to date, a
// newer release, or muted when nothing is known.
func updateRow(state string) aboutRow {
	r := aboutRow{Label: "updates", Value: state, Role: RoleMuted}
	switch {
	case strings.HasPrefix(state, "up to date"):
		r.Value, r.Role = GlyphDone+" "+state, RoleOK
	case strings.HasPrefix(state, "update ") && strings.Contains(state, " available"):
		r.Value, r.Role = GlyphUp+" "+state, RoleAttention
	}
	return r
}

// aboutLogo is the wordmark, two rows of half blocks: AGENT-BUS. Every row
// is aboutLogoW cells.
var aboutLogo = [2]string{
	"▄▀█ █▀▀ █▀▀ █▄ █ ▀█▀ ▄▄ █▄▄ █ █ █▀",
	"█▀█ █▄█ ██▄ █ ▀█  █     █▄█ █▄█ ▄█",
}

const (
	aboutLogoW  = 34
	aboutLabelW = 10 // "agentbus" and two cells
	aboutBlockW = 48 // the versions' column, at most
)

// aboutBody is the page in width cells, the packet at step; still is the
// drawing without motion. Everything is centred on the page: the wordmark
// (or the name in words when it does not fit), the versions in two aligned
// sections, then the bus line.
func aboutBody(a aboutState, step int, still bool, width int) []Line {
	var b body
	centre := func(w int) string { return strings.Repeat(" ", max(0, (width-w)/2)) }
	line := func(ss ...Seg) {
		w := 0
		for _, s := range ss {
			w += Width(s.Text)
		}
		b.text(centre(w)).segs(ss...).text("\n")
	}

	b.text("\n")
	if width >= aboutLogoW+2 {
		for _, row := range aboutLogo {
			line(Seg{Text: row, Role: RoleAccent, Bold: true})
		}
		line(Seg{Text: "m o n i t o r", Role: RoleAccent})
	} else {
		line(Seg{Text: "agent-bus monitor", Role: RoleAccent, Bold: true})
	}
	line(S(RoleMuted, "agents coordinating over Redis Streams"))
	b.text("\n")

	if !a.loaded {
		line(S(RoleMuted, "reading the versions"+GlyphEllipsis))
	} else {
		bw := min(aboutBlockW, max(0, width-4))
		pad := centre(bw)
		row := func(r aboutRow) {
			role := r.Role
			if role == RoleText && r.Value == "?" {
				role = RoleMuted
			}
			segs := []Seg{S(RoleMuted, Fit(r.Label, aboutLabelW)), S(role, Preview(r.Value))}
			if r.Note != "" {
				segs = append(segs, S(r.NoteRole, "  "+r.Note))
			}
			b.text(pad).segs(segs...).text("\n")
		}
		heading := func(name string) {
			rule := strings.Repeat(glyphHorizontal, max(0, bw-Width(name)-1))
			b.text(pad).seg(RoleAccent, name).seg(RoleLine, " "+rule).text("\n")
		}
		b.text(pad).seg(RoleMuted, Fit("project", aboutLabelW)).segs(Seg{Text: Preview(a.versions.Project), Role: RoleText, Bold: true}).text("\n\n")
		heading("THIS BUILD")
		for _, r := range a.versions.Build {
			row(r)
		}
		b.text("\n")
		heading("THIS HOST")
		for _, r := range a.versions.Host {
			row(r)
		}
	}
	b.text("\n")

	route := aboutRoute()
	packet, moving := aboutPoint{-1, -1}, !still && step > 0 && step < len(route)
	if moving {
		packet = route[step]
	}
	ox := centre(aboutBusW)
	gap := func(n int) string { return strings.Repeat(" ", max(0, n)) }
	b.text(ox).seg(RoleAccent, gap(aboutMasterX-2)+"master"+gap(aboutCoderX-aboutMasterX-6)+"coder").text("\n")
	for y := 0; y <= 2; y++ {
		var l Line
		for x := 0; x < aboutBusW; x++ {
			cell, role := " ", RoleMuted
			switch {
			case y == 2:
				cell, role = GlyphThinBar, RoleOK
			case x == aboutMasterX || x == aboutCoderX:
				cell, role = glyphVertical, RoleMuted
				if y == 0 {
					cell, role = GlyphGate, RoleAccent
				}
			}
			if moving && packet == (aboutPoint{x, y}) {
				cell, role = GlyphWorking, RoleAttention
			}
			l = append(l, S(role, cell))
		}
		b.text(ox).segs(l...).text("\n")
	}
	status := "bus ready"
	switch half := len(route) / 2; {
	case still:
	case step >= len(route):
		status = "delivered both ways"
	case step > half:
		status = "coder " + GlyphArrowL + " a reply to master"
	case step > 0:
		status = "master " + GlyphArrowR + " a directive to coder"
	}
	b.text("\n")
	line(S(RoleOK, status))
	if still {
		line(S(RoleMuted, "still: the packet travels with fade on, in true colour"))
	}
	var out []Line
	for _, l := range b.Lines() {
		out = append(out, FitLine(l, width))
	}
	return out
}

// realAboutSources reads the binaries beside busmon and the files of this
// host, each bounded.
func realAboutSources(getenv func(string) string) aboutSources {
	return aboutSources{
		agentbus: func() string {
			return versionLine(agentbusBeside(), []string{"version"}, "agentbus version ", "agentbus ?")
		},
		herdr: func() string {
			bin := getenv("HERDR_BIN_PATH")
			if bin == "" {
				bin = "herdr"
			}
			return versionLine(bin, []string{"--version"}, "herdr ", "herdr ?")
		},
		plugin: func() string { return pluginVersion(pluginDirs(getenv)) },
		update: func() string { return updateState(updateCachePath(), buildinfo.Version) },
	}
}

// versionLine is the first line of `bin args` starting with prefix, within
// 2 s, else unknown.
func versionLine(bin string, args []string, prefix, unknown string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return unknown
	}
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, prefix) {
			return strings.TrimSpace(l)
		}
	}
	return unknown
}

// pluginDirs are where herdr-plugin.toml may be, as agentbus up finds the
// repository: AGENTBUS_REPO, the build's repository, the binary's folder.
func pluginDirs(getenv func(string) string) []string {
	var dirs []string
	if d := getenv("AGENTBUS_REPO"); d != "" {
		dirs = append(dirs, d)
	}
	if buildinfo.Repo != "" {
		dirs = append(dirs, buildinfo.Repo)
	}
	if exe, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		dirs = append(dirs, filepath.Dir(exe))
	}
	return dirs
}

var pluginVersionRE = regexp.MustCompile(`(?m)^version\s*=\s*"([^"]+)"`)

// pluginVersion is the first herdr-plugin.toml's version among dirs, or ?.
func pluginVersion(dirs []string) string {
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(d, "herdr-plugin.toml"))
		if err != nil {
			continue
		}
		if m := pluginVersionRE.FindSubmatch(b); m != nil {
			return string(m[1])
		}
	}
	return "?"
}

// updateState is the update check's cached answer, never a network call: a
// newer release, up to date, or unknown.
func updateState(cachePath, version string) string {
	if version == "dev" {
		return "update not checked on a dev build"
	}
	var c updateCache
	b, err := os.ReadFile(cachePath)
	if cachePath == "" || err != nil || json.Unmarshal(b, &c) != nil || c.Tag == "" {
		return "update unknown (no check recorded)"
	}
	when := c.CheckedAt.Local().Format("Jan 2 15:04")
	if buildinfo.Newer(c.Tag, version) {
		return "update " + c.Tag + " available (checked " + when + ")"
	}
	return "up to date (" + c.Tag + ", checked " + when + ")"
}
