package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/history"
	"github.com/netbja/agent-bus-monitor/service"
)

// historyDeps is what agentbus history reads: the archive's directory ("" when
// the state directory is unknown) and the live streams (an error = the
// broker is unreachable, the archive alone is read).
type historyDeps struct {
	Dir  string
	Live func(ctx context.Context) ([]history.Entry, error)
	Now  func() time.Time
	Loc  *time.Location
	// Cap bounds the archive read when no --since narrows it (64 MiB when 0):
	// the newest days first, so 200 lines never read 256 MiB (foureyes).
	Cap int64
}

func realHistoryDeps(host, project string) historyDeps {
	d := historyDeps{Now: time.Now, Loc: time.Local}
	if paths, err := service.PathsFrom(os.Getenv); err == nil {
		d.Dir = history.Dir(paths.StateDir(project))
	}
	d.Live = func(ctx context.Context) ([]history.Entry, error) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		client, err := bus.ConnectContext(ctx, host)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		b, err := bus.Open(client, project)
		if err != nil {
			return nil, err
		}
		var out []history.Entry
		for _, s := range history.Streams {
			raw, err := b.StreamAfter(ctx, s, "", 20000)
			if err != nil {
				return nil, err
			}
			for _, r := range raw {
				out = append(out, history.Entry{Stream: s, ID: r.ID, F: r.Fields})
			}
		}
		return out, nil
	}
	return d
}

const historyUsage = "usage: history [--agent A] [--thread T] [--kind K] [--since D] [--until D] [--grep TEXT] [--limit N] [--json]"

// runHistory reads the archive, then the live streams for what is newer,
// merged by (stream, id), filtered, the newest --limit (200) printed oldest
// first.
func runHistory(args []string, d historyDeps, stdout, stderr io.Writer) int {
	var f history.Filter
	limit, asJSON := 200, false
	now := d.Now()
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--json" {
			asJSON = true
			continue
		}
		if i+1 >= len(args) {
			fmt.Fprintln(stderr, historyUsage)
			return 2
		}
		v := args[i+1]
		i++
		var err error
		switch a {
		case "--agent":
			f.Agent = v
		case "--thread":
			f.Thread = v
		case "--kind":
			f.Kind = v
		case "--grep":
			f.Grep = v
		case "--since":
			f.Since, err = parseWhen(v, false, now, d.Loc)
		case "--until":
			f.Until, err = parseWhen(v, true, now, d.Loc)
		case "--limit":
			limit, err = strconv.Atoi(v)
			if err == nil && limit < 0 {
				err = errors.New("want 0 (all) or more")
			}
		default:
			fmt.Fprintln(stderr, historyUsage)
			return 2
		}
		if err != nil {
			fmt.Fprintf(stderr, "history: %s %q: %v\n", a, v, err)
			return 2
		}
	}

	var archive []history.Entry
	if d.Dir != "" {
		until := f.Until
		if until.IsZero() {
			until = now
		}
		capBytes := int64(0)
		if f.Since.IsZero() {
			if capBytes = d.Cap; capBytes <= 0 {
				capBytes = 64 << 20
			}
		}
		var err error
		var cut bool
		if archive, cut, err = history.ReadDays(d.Dir, f.Since, until, capBytes); err != nil {
			fmt.Fprintf(stderr, "history: reading the archive: %v\n", err)
		} else if cut {
			fmt.Fprintf(stderr, "older archive not read (%d MiB read); use --since to reach further back\n", capBytes>>20)
		}
		gaps, err := history.ReadGaps(d.Dir) // every hole, whatever days were read (reviewer-codex)
		if err != nil {
			fmt.Fprintf(stderr, "history: reading the archive's gaps: %v\n", err)
		}
		archive = history.Merge(archive, gaps)
	}
	if _, err := os.Stat(d.Dir); d.Dir == "" || errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(stderr, "no archive on this host: live streams only")
	}
	live, err := d.Live(context.Background())
	if err != nil {
		fmt.Fprintln(stderr, "broker unreachable: archive only")
	}
	var shown []history.Entry
	for _, e := range history.Merge(archive, live) {
		if f.Match(e) {
			shown = append(shown, e)
		}
	}
	if limit > 0 && len(shown) > limit {
		shown = shown[len(shown)-limit:]
	}
	for _, e := range shown {
		if asJSON {
			b, _ := json.Marshal(e)
			fmt.Fprintln(stdout, string(b))
			continue
		}
		fmt.Fprintln(stdout, historyLine(e, d.Loc))
	}
	return 0
}

// historyLine is one entry as listen would print it, with its time.
func historyLine(e history.Entry, loc *time.Location) string {
	stamp := func(t time.Time) string { return t.In(loc).Format("Mon 01-02 15:04") }
	if e.Stream == history.GapStream {
		to := idTime(e.F["to"])
		if e.F["from"] == "unknown" {
			return fmt.Sprintf("-- archive restarted: nothing archived before %s --", stamp(to))
		}
		from := idTime(e.F["from"])
		end := to.In(loc).Format("15:04")
		if from.In(loc).Format("2006-01-02") != to.In(loc).Format("2006-01-02") {
			end = stamp(to)
		}
		return fmt.Sprintf("-- not archived: %s to %s (%s) --", stamp(from), end, e.F["of"])
	}
	who, text := historyWhoText(e)
	return fmt.Sprintf("%s  %s  %s  %s", stamp(history.At(e)), who, history.Kind(e), strings.Join(strings.Fields(text), " "))
}

func historyWhoText(e history.Entry) (who, text string) {
	f := e.F
	switch e.Stream {
	case "cmd":
		return f["from"] + " → " + f["target"], f["command"]
	case "report":
		return f["agent"], f["message"]
	case "status":
		return f["agent"], f["state"] + ": " + f["message"]
	case "notify":
		return f["from"], f["message"]
	case "verdicts":
		return f["reviewer"] + " → " + f["author"], f["decision"] + " " + f["subject"] + ": " + f["message"]
	case "health:log":
		return f["check"], f["previous"] + " → " + f["status"]
	case "swaps":
		return f["role"], f["event"]
	}
	return e.Stream, ""
}

func idTime(id string) time.Time {
	ms, err := strconv.ParseInt(strings.SplitN(id, "-", 2)[0], 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// parseWhen reads --since/--until: a duration back from now ("3d", "90m"), a
// date (its start, or for --until its end), a date and time, or a time today.
func parseWhen(s string, end bool, now time.Time, loc *time.Location) (time.Time, error) {
	if n, err := strconv.Atoi(strings.TrimSuffix(s, "d")); err == nil && strings.HasSuffix(s, "d") && n >= 0 {
		return now.AddDate(0, 0, -n), nil
	}
	if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return now.Add(-d), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		if end {
			return t.AddDate(0, 0, 1).Add(-time.Millisecond), nil
		}
		return t, nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	if t, err := time.ParseInLocation("15:04", s, loc); err == nil {
		y, m, dd := now.In(loc).Date()
		return time.Date(y, m, dd, t.Hour(), t.Minute(), 0, 0, loc), nil
	}
	return time.Time{}, errors.New("want a duration (3d, 90m), a date (2026-10-30), a date and time, or a time (14:20)")
}
