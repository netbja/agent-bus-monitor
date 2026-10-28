package bus

import (
	"context"
	"strconv"
	"strings"
)

// RedisInfo is what busmon's Services page says about the broker itself
// (read-only: INFO server and memory, DBSIZE).
type RedisInfo struct {
	Version    string // redis_version
	UptimeSecs int64  // uptime_in_seconds
	UsedMemory string // used_memory_human
	Keys       int64  // DBSIZE: every key of the database, all projects
	PID        int    // process_id
	Executable string // executable (Redis 7+)
}

// RedisInfo reads the broker's own facts.
func (b *Bus) RedisInfo(ctx context.Context) (RedisInfo, error) {
	var ri RedisInfo
	raw, err := b.r.Info(ctx, "server", "memory").Result()
	if err != nil {
		return ri, err
	}
	for _, line := range strings.Split(raw, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch k {
		case "redis_version":
			ri.Version = v
		case "uptime_in_seconds":
			ri.UptimeSecs, _ = strconv.ParseInt(v, 10, 64)
		case "used_memory_human":
			ri.UsedMemory = v
		case "process_id":
			ri.PID, _ = strconv.Atoi(v)
		case "executable":
			ri.Executable = v
		}
	}
	if ri.Keys, err = b.r.DBSize(ctx).Result(); err != nil {
		return ri, err
	}
	return ri, nil
}
