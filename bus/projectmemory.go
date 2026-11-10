package bus

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

// MemoryUsage is an estimate of what a project's keys take in the broker
// (projects-admin §3.3): Bytes over the Read keys of the Keys the scan
// found. Read < Keys when the deadline cut the reads short. ScanCut says the
// scan itself stopped ("cap": at the key cap; "deadline": at the caller's
// deadline), so Keys is a lower bound and the project's total is unknown.
// Refused: the broker refuses MEMORY (a restricted ACL), nothing is known.
type MemoryUsage struct {
	Bytes   int64
	Read    int
	Keys    int
	ScanCut string
	Refused bool
}

// memoryBatch is how many MEMORY USAGE calls one pipeline carries.
const memoryBatch = 100

// ProjectMemory estimates what project's keys take: MEMORY USAGE <key>
// with Redis's default sample of nested values (never SAMPLES 0, which
// walks every element of a big stream or hash on the broker, reviewer-
// codex), so each command's broker work is bounded. The {p}:* SCAN (the
// exact prefix, never a sibling) stops at max keys (0: no cap) and at ctx's
// end; the reads go memoryBatch at a time and stop at ctx's end too. A cut
// read returns what it read, never an error.
func ProjectMemory(ctx context.Context, r *redis.Client, project string, max int) (MemoryUsage, error) {
	if !ValidName(project) {
		return MemoryUsage{}, fmt.Errorf("invalid project %q (want %s)", project, nameRE)
	}
	var keys []string
	var u MemoryUsage
	for cursor := uint64(0); ; {
		if ctx.Err() != nil {
			u.ScanCut = "deadline"
			break
		}
		batch, next, err := r.Scan(ctx, cursor, project+":*", 500).Result()
		if err != nil {
			if ctx.Err() != nil {
				u.ScanCut = "deadline"
				break
			}
			return MemoryUsage{}, err
		}
		keys = append(keys, batch...)
		if max > 0 && len(keys) >= max {
			keys, u.ScanCut = keys[:max], "cap"
			break
		}
		if cursor = next; cursor == 0 {
			break
		}
	}
	u.Keys = len(keys)
	for i := 0; i < len(keys) && ctx.Err() == nil; i += memoryBatch {
		batch := keys[i:min(i+memoryBatch, len(keys))]
		pipe := r.Pipeline()
		cmds := make([]*redis.IntCmd, len(batch))
		for j, k := range batch {
			cmds[j] = pipe.MemoryUsage(ctx, k)
		}
		_, _ = pipe.Exec(ctx) // each command carries its own error
		for _, c := range cmds {
			n, err := c.Result()
			switch {
			case err == nil:
				u.Bytes += n
				u.Read++
			case errors.Is(err, redis.Nil): // gone since the scan: it takes nothing
				u.Read++
			case refusedCommand(err):
				return MemoryUsage{Keys: u.Keys, ScanCut: u.ScanCut, Refused: true}, nil
			case ctx.Err() != nil:
				return u, nil
			default:
				return u, err
			}
		}
	}
	return u, nil
}

// refusedCommand: the broker will not run the command for this client (an
// ACL without it, or a server that does not know it).
func refusedCommand(err error) bool {
	s := err.Error()
	return strings.HasPrefix(s, "NOPERM") || strings.Contains(s, "unknown command")
}
