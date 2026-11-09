package bus

import (
	"context"
	"errors"
	"strings"

	"github.com/redis/go-redis/v9"
)

// MemoryUsage is what a project's keys take in the broker (projects-admin
// §3.3): Bytes over the Read keys of Keys. Read < Keys when the cap or the
// caller's deadline cut the read short. Refused: the broker refuses MEMORY
// (a restricted ACL), so nothing is known.
type MemoryUsage struct {
	Bytes   int64
	Read    int
	Keys    int
	Refused bool
}

// memoryBatch is how many MEMORY USAGE calls one pipeline carries.
const memoryBatch = 500

// ProjectMemory sums MEMORY USAGE <key> SAMPLES 0 over project's keys
// (ProjectKeys: the exact {p}: prefix, never a sibling), at most max of them
// (0: no cap), pipelined memoryBatch at a time. It stops at ctx's end and
// returns what it read. One call per key: busmon calls it for one project,
// in the detail, never for the list.
func ProjectMemory(ctx context.Context, r *redis.Client, project string, max int) (MemoryUsage, error) {
	keys, err := ProjectKeys(ctx, r, project)
	if err != nil {
		return MemoryUsage{}, err
	}
	u := MemoryUsage{Keys: len(keys)}
	if max > 0 && len(keys) > max {
		keys = keys[:max]
	}
	for i := 0; i < len(keys) && ctx.Err() == nil; i += memoryBatch {
		batch := keys[i:min(i+memoryBatch, len(keys))]
		pipe := r.Pipeline()
		cmds := make([]*redis.IntCmd, len(batch))
		for j, k := range batch {
			cmds[j] = pipe.MemoryUsage(ctx, k, 0)
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
				return MemoryUsage{Keys: u.Keys, Refused: true}, nil
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
