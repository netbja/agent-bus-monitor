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

// aboutVersions are the lines y copies.
type aboutVersions struct{ Lines []string }

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
		return aboutVersionsMsg{serial: serial, v: aboutVersions{Lines: []string{
			"project " + project,
			"busmon " + buildinfo.Version,
			or(src.agentbus, "agentbus ?"),
			fmt.Sprintf("protocol v%d", bus.ProtocolVersion),
			"redis " + redis,
			or(src.herdr, "herdr ?"),
			"plugin " + or(src.plugin, "?"),
			or(src.update, "update unknown"),
			runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH,
		}}}
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

// aboutBody is the page in width cells, the packet at step; still is the
// drawing without motion.
func aboutBody(a aboutState, step int, still bool, width int) []Line {
	var b body
	b.text(" ").segs(Seg{Text: "agent-bus monitor", Role: RoleAccent, Bold: true}).text("\n")
	b.seg(RoleMuted, " agents coordinating over Redis Streams").text("\n\n")
	if !a.loaded {
		b.seg(RoleMuted, " reading the versions"+GlyphEllipsis).text("\n")
	}
	for _, l := range a.versions.Lines {
		b.seg(RoleText, " "+Preview(l)).text("\n")
	}
	b.text("\n")
	route := aboutRoute()
	packet, moving := aboutPoint{-1, -1}, !still && step > 0 && step < len(route)
	if moving {
		packet = route[step]
	}
	pad := func(n int) string { return strings.Repeat(" ", max(0, n)) }
	b.seg(RoleAccent, pad(aboutMasterX-2)+"master"+pad(aboutCoderX-aboutMasterX-6)+"coder").text("\n")
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
		b.text(" ").segs(l...).text("\n")
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
	b.text("\n").seg(RoleOK, " "+status).text("\n")
	if still {
		b.seg(RoleMuted, " still: the packet travels with fade on, in true colour").text("\n")
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
