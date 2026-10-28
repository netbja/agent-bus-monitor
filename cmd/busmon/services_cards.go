package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/service"
)

// F3 Services as cards (busmon-f3, Bernard): dispatch, missions and Redis,
// side by side when wide, stacked otherwise. Every fact is gathered in the
// command that opens the page (gatherServices: the records, the logs' sizes,
// the processes' start, the broker's INFO and, when docker is on PATH, its
// container); the cards are a pure function of those facts. A service card's
// raw log is a page of its own (Enter or l).

// redisContainer is the broker's container name (docker-compose.yml, setup.sh).
const redisContainer = "agent-bus-redis"

// redisInfoReader is the broker's own facts (a *bus.Bus).
type redisInfoReader interface {
	RedisInfo(ctx context.Context) (bus.RedisInfo, error)
}

// svcFacts is everything the Services page shows.
type svcFacts struct {
	inst  *bus.ServicesInstall
	recs  map[string]bus.ServiceRecord
	host  string
	logs  map[string]int64     // service -> its current log's size (absent: unknown)
	since map[string]time.Time // service -> its process's start (this host only)
	redis redisFacts
}

type redisFacts struct {
	where   string // "docker agent-bus-redis (redis:8-alpine), running", or "process 812 (/usr/bin/redis-server)"
	info    bus.RedisInfo
	infoErr string
}

// svcRunner runs docker; nil = no docker call at all.
type svcRunner func(ctx context.Context, name string, args ...string) (string, error)

func realSvcRunner(ctx context.Context, name string, args ...string) (string, error) {
	if _, err := exec.LookPath(name); err != nil {
		return "", err
	}
	tail := &tailBuffer{max: svcOutMax}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = tail // drained as it comes: memory stays within the bound, whatever the command prints
	err := cmd.Run()
	return strings.TrimSpace(string(tail.buf)), err
}

// svcOutMax bounds what a docker read keeps: the end of its output.
const svcOutMax = redisLogBytes

// tailBuffer keeps only the last max bytes written to it.
type tailBuffer struct {
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) >= t.max {
		t.buf = append(t.buf[:0], p[len(p)-t.max:]...)
		return n, nil
	}
	if over := len(t.buf) + len(p) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	t.buf = append(t.buf, p...)
	return n, nil
}

// gatherServices reads the facts; it runs in a command, never in View.
func gatherServices(ctx context.Context, r busReader, project string, inst *bus.ServicesInstall, recs map[string]bus.ServiceRecord, host string, run svcRunner) svcFacts {
	f := svcFacts{inst: inst, recs: recs, host: host, logs: map[string]int64{}, since: map[string]time.Time{}}
	for k, rec := range recs {
		if rec.Host != host {
			continue // its log and process are on another machine
		}
		if info, err := os.Stat(rec.Log); err == nil && rec.Log != "" {
			f.logs[k] = info.Size()
		}
		if t, ok := serviceStart(project, k, rec, "/proc"); ok {
			f.since[k] = t
		}
	}
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if ri, ok := r.(redisInfoReader); ok {
		info, err := ri.RedisInfo(c)
		f.redis.info = info
		if err != nil {
			f.redis.infoErr = err.Error()
		}
	} else {
		f.redis.infoErr = "not read"
	}
	if run != nil {
		if out, err := run(c, "docker", "inspect", "-f", "{{.Config.Image}}|{{.State.Status}}", redisContainer); err == nil && out != "" {
			image, status, _ := strings.Cut(out, "|")
			f.redis.where = fmt.Sprintf("docker %s (%s), %s", redisContainer, image, status)
		}
	}
	if f.redis.where == "" && f.redis.info.PID > 0 {
		f.redis.where = fmt.Sprintf("process %d", f.redis.info.PID)
		if f.redis.info.Executable != "" {
			f.redis.where += " (" + f.redis.info.Executable + ")"
		}
	}
	return f
}

// serviceStart is a running service's process start, read only when the
// record says it runs and the pid's command line is that service (a stopped
// service's pid may have been reused): /proc/<pid>/stat's start (field 22,
// clock ticks since boot) plus /proc/stat's btime. Anything else: unknown.
func serviceStart(project, kind string, rec bus.ServiceRecord, proc string) (time.Time, bool) {
	switch rec.State {
	case "", "down", "stopped", "failed", "gave-up":
		return time.Time{}, false
	}
	if rec.PID <= 0 {
		return time.Time{}, false
	}
	cmd, err := os.ReadFile(fmt.Sprintf("%s/%d/cmdline", proc, rec.PID))
	if err != nil || !isServiceArgv(strings.Split(strings.TrimRight(string(cmd), "\x00"), "\x00"), project, kind) {
		return time.Time{}, false
	}
	stat, err := os.ReadFile(fmt.Sprintf("%s/%d/stat", proc, rec.PID))
	if err != nil {
		return time.Time{}, false
	}
	s := string(stat)
	end := strings.LastIndexByte(s, ')') // the command name may hold spaces
	if end < 0 {
		return time.Time{}, false
	}
	fields := strings.Fields(s[end+1:]) // field 3 onward
	if len(fields) < 20 {
		return time.Time{}, false
	}
	var ticks int64
	if _, err := fmt.Sscan(fields[19], &ticks); err != nil { // field 22
		return time.Time{}, false
	}
	boot, err := os.ReadFile(proc + "/stat")
	if err != nil {
		return time.Time{}, false
	}
	for _, line := range strings.Split(string(boot), "\n") {
		var btime int64
		if n, _ := fmt.Sscanf(line, "btime %d", &btime); n == 1 {
			return time.Unix(btime, 0).Add(time.Duration(ticks) * time.Second / 100), true // USER_HZ 100 on Linux
		}
	}
	return time.Time{}, false
}

// isServiceArgv: argv is this project's service of this kind,
// "agentbus --project <project> service run <kind>" (services are per project).
func isServiceArgv(argv []string, project, kind string) bool {
	proj := ""
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "--project" {
			proj = argv[i+1]
		}
		if argv[i] == "service" && i+2 < len(argv) && argv[i+1] == "run" && argv[i+2] == kind {
			return proj == project && project != ""
		}
	}
	return false
}

// svcCardKinds are the selectable cards, in order (Redis has no log page).
var svcCardKinds = service.Kinds

// servicesCards is the page body at width cells, the card sel selected.
func servicesCards(f svcFacts, sel int, now time.Time, width int) []Line {
	var out []Line
	rule := strings.Repeat(GlyphDash, 2)
	if f.inst != nil {
		out = append(out, Line{S(RoleMuted, fmt.Sprintf("%s %s services on %s, set up %s ago %s", rule, Preview(f.inst.Manager), Preview(f.inst.Host),
			humanAge(now.Sub(time.UnixMilli(f.inst.At))), rule))}, nil)
	} else if len(f.recs) == 0 {
		out = append(out, Line{S(RoleMuted, rule+" no project services recorded: dispatch and missions run as herdr tabs, or not at all "+rule)}, nil)
	}
	var cards [][]Line
	for i, k := range svcCardKinds {
		cards = append(cards, serviceCard(f, k, i == sel, now))
	}
	cards = append(cards, redisCard(f.redis))
	cols := 1
	if cardsSideBySide(width) {
		cols = len(cards)
	}
	cw := (width - (cols - 1)) / cols
	height := 0
	for _, c := range cards {
		height = max(height, len(c))
	}
	boxed := make([][]Line, len(cards))
	for i, c := range cards {
		for len(c) < height && cols > 1 { // side by side: one height
			c = append(c, nil)
		}
		boxed[i] = boxCard(c, cw, i == sel)
	}
	if cols == 1 {
		for _, b := range boxed {
			out = append(out, b...)
		}
	} else {
		for row := range boxed[0] {
			var l Line
			for i, b := range boxed {
				if i > 0 {
					l = append(l, S(RoleText, " "))
				}
				l = append(l, b[row]...)
			}
			out = append(out, l)
		}
	}
	out = append(out, nil, Line{S(RoleMuted, "enter or l: the selected service's log, enter on Redis: its page  "+GlyphNone+"  agentbus down <p> stops them; agentbus --project <p> service status|logs|restart")})
	for i := range out {
		out[i] = FitLine(out[i], width)
	}
	return out
}

// serviceCard is one service's lines: title (state), pid and since, its
// summary, its log and size.
func serviceCard(f svcFacts, kind string, selected bool, now time.Time) []Line {
	r, ok := f.recs[kind]
	if !ok {
		return []Line{{S(RoleAccent, kind)}, {S(RoleMuted, "no state published")}, {S(RoleMuted, "(not started since agentbus up)")}}
	}
	state := serviceFact(r, true, "", now)
	role := map[string]Role{"failed": RoleError, "gave-up": RoleError, "waiting-herdr": RoleAttention, "waiting-git": RoleAttention, "starting": RoleAttention, "down": RoleMuted, "stopped": RoleMuted}[state]
	glyph := GlyphWorking
	if role == RoleText {
		role = RoleOK
	} else if role == RoleError || role == RoleMuted {
		glyph = GlyphBlocked
	}
	lines := []Line{{S(RoleAccent, kind+"  "), S(role, glyph+" "+Preview(state))}}
	pid := "pid ?"
	if r.PID > 0 {
		pid = fmt.Sprintf("pid %d", r.PID)
	}
	if t, ok := f.since[kind]; ok {
		pid += fmt.Sprintf(" %s since %s (%s)", GlyphNone, t.In(time.Local).Format("15:04"), humanAge(now.Sub(t)))
	} else {
		pid += fmt.Sprintf(" %s updated %s ago", GlyphNone, humanAge(now.Sub(time.UnixMilli(r.At))))
	}
	if r.Host != f.host {
		pid += " on " + Preview(r.Host)
	}
	lines = append(lines, Line{S(RoleMuted, pid)})
	if r.Detail != "" {
		lines = append(lines, Line{S(RoleText, Preview(r.Detail))})
	}
	if r.Error != "" {
		lines = append(lines, Line{S(RoleError, "last error: "), S(RoleText, Preview(r.Error))})
	}
	size := "?"
	if n, ok := f.logs[kind]; ok {
		size = humanBytes(n)
	}
	lines = append(lines, Line{S(RoleMuted, fmt.Sprintf("log %s of %s, %d kept", size, humanBytes(service.LogMax), service.LogKeep))})
	if r.Log != "" {
		lines = append(lines, Line{S(RoleMuted, Preview(r.Log))})
	}
	return lines
}

// redisCard: where the broker runs, its version and uptime, memory and keys.
func redisCard(r redisFacts) []Line {
	q := func(s string) string {
		if s == "" {
			return "?"
		}
		return s
	}
	known := r.infoErr == "" && r.info.Version != "" // never read is unknown, never 0
	lines := []Line{{S(RoleAccent, "redis  "), S(RoleOK, GlyphWorking+" up")}}
	if !known {
		lines[0] = Line{S(RoleAccent, "redis  "), S(RoleError, GlyphBlocked+" ?")}
	}
	lines = append(lines, Line{S(RoleText, Preview(q(r.where)))})
	up := "?"
	if known {
		up = humanAge(time.Duration(r.info.UptimeSecs) * time.Second)
	}
	lines = append(lines, Line{S(RoleMuted, fmt.Sprintf("version %s %s up %s", q(r.info.Version), GlyphNone, up))})
	keys := "?"
	if known {
		keys = fmt.Sprint(r.info.Keys)
	}
	lines = append(lines, Line{S(RoleMuted, fmt.Sprintf("memory %s %s %s keys (every project)", q(r.info.UsedMemory), GlyphNone, keys))})
	if r.infoErr != "" {
		lines = append(lines, Line{S(RoleError, Preview(r.infoErr))})
	}
	return lines
}

// boxCard frames lines in a w-cell box, its border accented when selected.
func boxCard(lines []Line, w int, selected bool) []Line {
	role := RoleLine
	if selected {
		role = RoleAccent
	}
	inner := max(1, w-4)
	out := []Line{{S(role, glyphTopLeft+strings.Repeat(glyphHorizontal, max(0, w-2))+glyphTopRight)}}
	for _, l := range lines {
		out = append(out, append(append(Line{S(role, glyphVertical+" ")}, FitLine(l, inner)...), S(role, " "+glyphVertical)))
	}
	return append(out, Line{S(role, glyphBottomLeft+strings.Repeat(glyphHorizontal, max(0, w-2))+glyphBottomRight)})
}

// humanBytes: 0 B, 812 B, 331 KB, 1 MB.
func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%d KB", n/1024)
	}
	return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
}

// cardsSideBySide is whether the F3 cards sit in one row (at least 110
// cells): then ←/→ move between them as ↑/↓ do (busmon-f3-keys).
func cardsSideBySide(width int) bool { return width >= 110 }
