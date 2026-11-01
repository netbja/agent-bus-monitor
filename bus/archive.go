package bus

import (
	"context"
	"fmt"
	"regexp"
)

// RawEntry is a stream entry as Redis holds it: its id and its string fields.
type RawEntry struct {
	ID     string
	Fields map[string]string
}

// streamKind is a project stream's suffix: "cmd", "health:log".
var streamKind = regexp.MustCompile(`^[a-z]+(:[a-z]+)?$`)

// StreamAfter returns at most count entries of the project's stream kind newer
// than after ("" = from the oldest), oldest first: the history archive's read.
func (b *Bus) StreamAfter(ctx context.Context, kind, after string, count int64) ([]RawEntry, error) {
	if !streamKind.MatchString(kind) {
		return nil, fmt.Errorf("invalid stream kind %q", kind)
	}
	start := "-"
	if after != "" {
		start = "(" + after
	}
	msgs, err := b.r.XRangeN(ctx, StreamKey(b.project, kind), start, "+", count).Result()
	if err != nil {
		return nil, err
	}
	out := make([]RawEntry, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, RawEntry{ID: m.ID, Fields: toStringMap(m.Values)})
	}
	return out, nil
}

// StreamOldest is the oldest id the project's stream kind retains; ok is false
// for an empty or absent stream.
func (b *Bus) StreamOldest(ctx context.Context, kind string) (string, bool, error) {
	es, err := b.StreamAfter(ctx, kind, "", 1)
	if err != nil || len(es) == 0 {
		return "", false, err
	}
	return es[0].ID, true, nil
}
