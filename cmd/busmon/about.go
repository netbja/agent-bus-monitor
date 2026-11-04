package main

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/buildinfo"
	"github.com/netbja/agent-bus-monitor/bus"
)

// About (A, about-page): the versions (y copies them), then two agents on a
// bus line, a packet travelling from master to coder and back; r replays.
// The versions are read in a command; each step is a tick of its own run,
// so an earlier run's tick or a closed page's ends its chain.

// aboutPoint is a cell of the drawing: x a column, y a row (0 the agents'
// nodes, 1 their stems, 2 the bus).
type aboutPoint struct{ x, y int }

const (
	aboutMasterX = 6
	aboutCoderX  = 26
	aboutBusW    = 33
	aboutEvery   = 45 * time.Millisecond
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

// aboutState is the page's run.
type aboutState struct {
	step, gen int
	versions  aboutVersions
	loaded    bool
}

type aboutTickMsg struct{ serial, gen int }

type aboutVersionsMsg struct {
	serial int
	v      aboutVersions
}

func aboutTick(serial, gen int) tea.Cmd {
	return tea.Tick(aboutEvery, func(time.Time) tea.Msg { return aboutTickMsg{serial: serial, gen: gen} })
}

// openAbout opens the page, reads the versions and starts the run.
func (m *Model) openAbout() tea.Cmd {
	m.push(page{kind: ovAbout, about: &aboutState{}})
	serial := m.rd.top().serial
	r, ctx, ab := m.cfg.reader, m.cfg.ctx, m.cfg.aboutAgentbus
	if ctx == nil {
		ctx = context.Background()
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
		agentbus := "agentbus ?"
		if ab != nil {
			agentbus = ab()
		}
		return aboutVersionsMsg{serial: serial, v: aboutVersions{Lines: []string{
			"busmon " + buildinfo.Version,
			agentbus,
			fmt.Sprintf("protocol v%d", bus.ProtocolVersion),
			"redis " + redis,
			runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH,
		}}}
	}
	return tea.Batch(read, aboutTick(serial, 0))
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

// gotAboutTick moves the packet one cell; the run's last step schedules
// nothing.
func (m *Model) gotAboutTick(msg aboutTickMsg) tea.Cmd {
	p := m.aboutPage(msg.serial)
	if p == nil || p.about.gen != msg.gen || p.about.step >= len(aboutRoute()) {
		return nil
	}
	p.about.step++
	p.dirty = true
	m.refresh()
	if p.about.step >= len(aboutRoute()) {
		return nil
	}
	return aboutTick(msg.serial, msg.gen)
}

// aboutReplay is r: a new run from the start.
func (m *Model) aboutReplay(p *page) tea.Cmd {
	p.about.gen++
	p.about.step, p.dirty = 0, true
	m.refresh()
	return aboutTick(p.serial, p.about.gen)
}

// aboutBody is the page in width cells.
func aboutBody(a aboutState, width int) []Line {
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
	packet, moving := aboutPoint{-1, -1}, a.step > 0 && a.step < len(route)
	if moving {
		packet = route[a.step]
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
	case a.step >= len(route):
		status = "delivered both ways"
	case a.step > half:
		status = "coder " + GlyphArrowL + " a reply to master"
	case a.step > 0:
		status = "master " + GlyphArrowR + " a directive to coder"
	}
	b.text("\n").seg(RoleOK, " "+status).text("\n")
	var out []Line
	for _, l := range b.Lines() {
		out = append(out, FitLine(l, width))
	}
	return out
}

// realAboutAgentbus is `agentbus version`'s version line, from the agentbus
// beside busmon, 2 s at most.
func realAboutAgentbus(bin string) func() string {
	return func() string {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, bin, "version").Output()
		if err != nil {
			return "agentbus ?"
		}
		for _, l := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(l, "agentbus version ") {
				return strings.TrimSpace(l)
			}
		}
		return "agentbus ?"
	}
}
