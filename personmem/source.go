package personmem

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/history"
)

// Source names a bus entry: --from <project>:<entry>.
type Source struct {
	Project string
	ID      string
}

func (s Source) String() string { return s.Project + ":" + s.ID }

// ParseSource reads "<project>:<entry>".
func ParseSource(s string) (Source, error) {
	p, id, ok := strings.Cut(s, ":")
	if !ok || !bus.ValidName(p) || !validEntryID(id) {
		return Source{}, fmt.Errorf("%q is not <project>:<entry id>", s)
	}
	return Source{Project: p, ID: id}, nil
}

// validEntryID: a stream id, <ms>-<seq>.
func validEntryID(id string) bool {
	ms, seq, ok := strings.Cut(id, "-")
	_, e1 := strconv.ParseUint(ms, 10, 64)
	_, e2 := strconv.ParseUint(seq, 10, 64)
	return ok && e1 == nil && e2 == nil
}

// entryTime is the time in an entry's id (Redis TIME when it was written).
func entryTime(id string) time.Time {
	ms, _, _ := strings.Cut(id, "-")
	n, _ := strconv.ParseInt(ms, 10, 64)
	return time.UnixMilli(n).UTC()
}

// Entry is a bus entry as remember reads it.
type Entry struct {
	Kind  string // cmd, notify or report
	Type  string // cmd: directive, reply...
	From  string // the sender (a report's agent)
	Body  string // cmd: command; notify and report: message
	Full  string // report: its full text
	Said  string // an ask answer's own words
	Human bool
	Typed bool // the person typed it (spec §4.3.1)
}

// Reader finds an entry; ok false when neither the stream nor the archive
// holds it.
type Reader interface {
	Entry(ctx context.Context, s Source) (Entry, bool, error)
}

// BusReader reads an entry from its project's streams, then from its history
// archive once a stream has trimmed it (spec §4.3.1). HistoryDir is the
// project's archive directory (history.Dir of its state directory).
type BusReader struct {
	Client     *redis.Client
	HistoryDir func(project string) string
}

// readKinds are the streams a person's words, or a proposal, live in.
var readKinds = []string{"cmd", "notify", "report"}

func (r BusReader) Entry(ctx context.Context, s Source) (Entry, bool, error) {
	b, err := bus.Open(r.Client, s.Project)
	if err != nil {
		return Entry{}, false, err
	}
	for _, k := range readKinds {
		e, ok, err := b.EntryByID(ctx, k, s.ID)
		if err != nil {
			return Entry{}, false, err
		}
		if ok {
			return entryOf(e), true, nil
		}
	}
	if r.HistoryDir == nil {
		return Entry{}, false, nil
	}
	day := entryTime(s.ID)
	es, _, err := history.ReadDays(r.HistoryDir(s.Project), day.Add(-24*time.Hour), day.Add(24*time.Hour), 0)
	if err != nil {
		return Entry{}, false, err
	}
	for _, h := range es {
		for _, k := range readKinds {
			if h.ID == s.ID && h.Stream == k {
				return entryOf(bus.ParseEntry(bus.StreamKey(s.Project, k), h.ID, h.F)), true, nil
			}
		}
	}
	return Entry{}, false, nil
}

func entryOf(e bus.Event) Entry {
	from := e.From
	if e.Kind == "report" {
		from = e.Agent
	}
	return Entry{Kind: e.Kind, Type: e.Type, From: from, Body: e.Message, Full: e.Full, Said: e.Said, Human: e.Human, Typed: e.Typed}
}
