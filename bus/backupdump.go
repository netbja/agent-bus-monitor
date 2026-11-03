package bus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// DumpedKey is one key of a project's backup (spec "Redis"): its type, its
// class and the server's own DUMP payload.
type DumpedKey struct {
	Key   string
	Type  string
	Class KeyClass
	Dump  []byte
}

// ProjectDump is a project's keys read at one instant.
type ProjectDump struct {
	Keys     []DumpedKey
	Expiring []string      // keys skipped for having an expiry
	Exec     time.Duration // how long the server served nobody else
}

// keepDump: only a key with no expiry (PTTL -1) is kept. A key a millisecond
// from expiring (0) is skipped too, so no lease comes back persistent; -2 is a
// key gone since the SCAN.
func keepDump(pttl time.Duration) bool { return pttl == -1 }

// DumpProject reads every key of project (ProjectKeys' exact prefix: a
// sibling is never read) in one MULTI of TYPE, PTTL and DUMP per key, so the
// keys are read at one instant and no write lands between two of them. A key
// created after the SCAN is not in it; one deleted after it is skipped.
func DumpProject(ctx context.Context, r *redis.Client, project string) (ProjectDump, error) {
	keys, err := ProjectKeys(ctx, r, project)
	if err != nil {
		return ProjectDump{}, err
	}
	var d ProjectDump
	if len(keys) == 0 {
		return d, nil
	}
	types := make([]*redis.StatusCmd, len(keys))
	ttls := make([]*redis.DurationCmd, len(keys))
	dumps := make([]*redis.StringCmd, len(keys))
	start := time.Now()
	_, err = r.TxPipelined(ctx, func(p redis.Pipeliner) error {
		for i, k := range keys {
			types[i], ttls[i], dumps[i] = p.Type(ctx, k), p.PTTL(ctx, k), p.Dump(ctx, k)
		}
		return nil
	})
	d.Exec = time.Since(start)
	if err != nil && !errors.Is(err, redis.Nil) { // a key deleted since the SCAN DUMPs nil
		return ProjectDump{}, fmt.Errorf("dump %s: %w", project, err)
	}
	for i, k := range keys {
		typ, pttl := types[i].Val(), ttls[i].Val()
		if typ == "none" || pttl == -2 {
			continue
		}
		if !keepDump(pttl) {
			d.Expiring = append(d.Expiring, k)
			continue
		}
		payload, err := dumps[i].Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return ProjectDump{}, fmt.Errorf("dump %s: %w", k, err)
		}
		class, _ := ClassOf(project, k)
		d.Keys = append(d.Keys, DumpedKey{Key: k, Type: typ, Class: class, Dump: []byte(payload)})
	}
	return d, nil
}
