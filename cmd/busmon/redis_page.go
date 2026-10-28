package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The Redis card's page (busmon-redis-detail): Enter on F3's Redis card
// opens what the card summarises, read once when the page opens: the
// broker's INFO and per-project footprint (bus.RedisDetail), the published
// port and whether it stays on loopback, whether the default password is in
// use, and the tail of the container's log. Read-only: nothing here writes
// to Redis or to docker.

// redisDetailReader is the optional reader behind the page.
type redisDetailReader interface {
	RedisDetail(ctx context.Context) (bus.RedisDetail, error)
}

// redisLogLines bounds the docker log tail, and redisLogBytes what is read.
const (
	redisLogLines = 20
	redisLogBytes = 8 << 10
)

// redisPage is what the page shows; an empty field with its error set reads ?.
type redisPage struct {
	detail   bus.RedisDetail
	err      string // the INFO read failed: nothing below it is known
	bind     string // docker port's answer, one address per line joined by ", "
	loopback bool   // every published address is 127.0.0.1 or [::1]
	bindErr  string
	legacyPW bool // this busmon connects with the default password
	logs     []string
	logsErr  string
}

// redisPageMsg carries the page's body, built in the command that read it.
type redisPageMsg struct{ body []Line }

// gatherRedisPage reads the page's facts; it runs in a command, never in
// View. run nil means no docker call at all.
func gatherRedisPage(ctx context.Context, r any, run svcRunner, getenv func(string) string) redisPage {
	var f redisPage
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if dr, ok := r.(redisDetailReader); ok {
		d, err := dr.RedisDetail(c)
		f.detail = d
		if err != nil {
			f.err = err.Error()
		}
	} else {
		f.err = "not read"
	}
	f.legacyPW = bus.LegacyPasswordInUse(getenv)
	if run == nil {
		f.bindErr, f.logsErr = "no docker", "no docker"
		return f
	}
	if out, err := run(c, "docker", "port", redisContainer, "6379/tcp"); err != nil || out == "" {
		f.bindErr = "not published by docker"
		if err != nil {
			f.bindErr = err.Error()
		}
	} else {
		lines := strings.Fields(out)
		f.bind = strings.Join(lines, ", ")
		f.loopback = true
		for _, a := range lines {
			if !strings.HasPrefix(a, "127.0.0.1:") && !strings.HasPrefix(a, "[::1]:") {
				f.loopback = false
			}
		}
	}
	out, err := run(c, "docker", "logs", "--tail", fmt.Sprint(redisLogLines), redisContainer)
	if err != nil {
		f.logsErr = err.Error()
		return f
	}
	if len(out) > redisLogBytes {
		out = out[len(out)-redisLogBytes:]
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > redisLogLines {
		lines = lines[len(lines)-redisLogLines:]
	}
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			f.logs = append(f.logs, Preview(l))
		}
	}
	return f
}

// redisPageBody is the page, pure.
func redisPageBody(f redisPage, now time.Time, width int) []Line {
	sep := "  " + GlyphNone + "  "
	q := func(s string) string {
		if s == "" {
			return "?"
		}
		return s
	}
	var out []Line
	d := f.detail
	if f.err != "" {
		out = append(out, Line{S(RoleAccent, "redis  "), S(RoleError, GlyphBlocked+" ?  "+Preview(f.err))})
	} else {
		out = append(out,
			Line{S(RoleAccent, "redis  "), S(RoleOK, GlyphWorking+" "+q(d.Info.Version)),
				S(RoleMuted, sep+"up "+humanAge(time.Duration(d.Info.UptimeSecs)*time.Second))},
			nil)
		limit := d.MaxMemory
		if limit == "0B" || limit == "0" {
			limit = "no limit"
		}
		out = append(out, Line{S(RoleText, "memory "+q(d.Info.UsedMemory)+sep+"peak "+q(d.PeakMemory)+sep+"max "+q(limit)+" ("+q(d.MaxMemoryPolicy)+")")})
		rdb := "never"
		if d.RDBLastSave > 0 {
			rdb = humanAge(now.Sub(time.Unix(d.RDBLastSave, 0))) + " ago"
		}
		aof := "AOF off"
		if d.AOFEnabled {
			aof = "AOF on (" + q(d.AOFStatus) + ")"
		}
		out = append(out, Line{S(RoleText, "persistence: last RDB save "+rdb+" ("+q(d.RDBStatus)+")"+sep+aof)})
		out = append(out, Line{S(RoleText, fmt.Sprintf("%d clients%s%d keys (every project)", d.Clients, sep, d.Info.Keys))})
	}
	switch {
	case f.bindErr != "":
		out = append(out, Line{S(RoleMuted, "bind ?  ("+Preview(f.bindErr)+")")})
	case f.loopback:
		out = append(out, Line{S(RoleText, "bind "+Preview(f.bind)+sep), S(RoleOK, "loopback only")})
	default:
		out = append(out, Line{S(RoleText, "bind "+Preview(f.bind)+sep), S(RoleError, GlyphBlocked+" published beyond loopback")})
	}
	if f.legacyPW {
		out = append(out, Line{S(RoleAttention, "password: the default password is in use (agentbus health detail redis-password)")})
	} else {
		out = append(out, Line{S(RoleMuted, "password: not the default")})
	}
	if f.err == "" {
		out = append(out, nil, Line{S(RoleMuted, "projects")})
		switch {
		case d.ProjectsErr != "":
			out = append(out, Line{S(RoleError, "?  "+Preview(d.ProjectsErr))})
		case len(d.Projects) == 0:
			out = append(out, Line{S(RoleMuted, "none")})
		}
		for _, p := range d.Projects {
			var parts []string
			for _, s := range bus.DetailStreams() {
				parts = append(parts, fmt.Sprintf("%s %d", s, p.Streams[s]))
			}
			out = append(out, Line{S(RoleAccent, Preview(p.Project)), S(RoleText, fmt.Sprintf("  %d keys%s%s", p.Keys, sep, strings.Join(parts, sep)))})
		}
	}
	out = append(out, nil, Line{S(RoleMuted, fmt.Sprintf("docker logs (last %d)", redisLogLines))})
	switch {
	case f.logsErr != "":
		out = append(out, Line{S(RoleMuted, "logs ?  ("+Preview(f.logsErr)+")")})
	case len(f.logs) == 0:
		out = append(out, Line{S(RoleMuted, "empty")})
	}
	for _, l := range f.logs {
		out = append(out, Line{S(RoleText, l)})
	}
	for i := range out {
		out[i] = FitLine(out[i], width)
	}
	return out
}

// redisPageCmd reads the page's facts and builds its body.
func (m *Model) redisPageCmd() tea.Cmd {
	r, ctx, width := m.cfg.reader, m.cfg.ctx, m.readerWidth()
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		f := gatherRedisPage(ctx, r, realSvcRunner, os.Getenv)
		return redisPageMsg{body: redisPageBody(f, time.Now(), width)}
	}
}
