package bus

import (
	"context"
	"encoding/json"
)

// boardSet writes a whole board entry from a struct: a test helper only.
// Production code never does (TestNoStructWriterOfTheBoard), since a struct
// round trip drops a field the struct does not know (request-priority).
func (b *Bus) boardSet(ctx context.Context, key, task string, e BoardEntry) error {
	v, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return b.r.HSet(ctx, key, task, v).Err()
}
