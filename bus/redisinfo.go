package bus

import (
	"context"
	"slices"
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

// RedisDetail is the Redis card's page (busmon-redis-detail): the card's
// facts, the memory limits, persistence and clients, and per project its
// keys and stream lengths. Read-only: INFO, DBSIZE, SCAN and XLEN.
type RedisDetail struct {
	Info            RedisInfo
	PeakMemory      string // used_memory_peak_human
	MaxMemory       string // maxmemory_human ("0B" = no limit)
	MaxMemoryPolicy string // maxmemory_policy
	RDBLastSave     int64  // rdb_last_save_time, Unix seconds (0 = never)
	RDBStatus       string // rdb_last_bgsave_status
	AOFEnabled      bool   // aof_enabled
	AOFStatus       string // aof_last_write_status
	Clients         int    // connected_clients
	Projects        []ProjectRedis
	ProjectsErr     string // the project scan failed: the rows are unknown, never empty
	Peer            string // the address this client's socket reached (PeerAddr), "" unknown
}

// ProjectRedis is one project's footprint on the broker.
type ProjectRedis struct {
	Project string
	Keys    int              // every {p}:* key
	Streams map[string]int64 // status, report, notify, cmd, verdicts -> XLEN
}

// detailStreams are the streams whose lengths the page shows, in this order.
var detailStreams = []string{"status", "report", "notify", "cmd", "verdicts"}

// RedisDetail reads the broker's detail page. An unreadable project scan is
// stated in ProjectsErr; the INFO part fails the call, as RedisInfo does.
func (b *Bus) RedisDetail(ctx context.Context) (RedisDetail, error) {
	d := RedisDetail{Peer: PeerAddr(b.r)}
	info, err := b.RedisInfo(ctx)
	d.Info = info
	if err != nil {
		return d, err
	}
	raw, err := b.r.Info(ctx, "memory", "persistence", "clients").Result()
	if err != nil {
		return d, err
	}
	for _, line := range strings.Split(raw, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch k {
		case "used_memory_peak_human":
			d.PeakMemory = v
		case "maxmemory_human":
			d.MaxMemory = v
		case "maxmemory_policy":
			d.MaxMemoryPolicy = v
		case "rdb_last_save_time":
			d.RDBLastSave, _ = strconv.ParseInt(v, 10, 64)
		case "rdb_last_bgsave_status":
			d.RDBStatus = v
		case "aof_enabled":
			d.AOFEnabled = v == "1"
		case "aof_last_write_status":
			d.AOFStatus = v
		case "connected_clients":
			d.Clients, _ = strconv.Atoi(v)
		}
	}
	projects, err := Projects(ctx, b.r)
	if err != nil {
		d.ProjectsErr = err.Error()
		return d, nil
	}
	for _, p := range projects {
		row := ProjectRedis{Project: p.Project, Streams: map[string]int64{}}
		keys, err := ProjectKeys(ctx, b.r, p.Project)
		if err != nil {
			d.Projects, d.ProjectsErr = nil, err.Error() // no partial rows beside the ?
			return d, nil
		}
		row.Keys = len(keys)
		for _, s := range detailStreams {
			n, err := b.r.XLen(ctx, p.Project+":"+s).Result()
			if err != nil {
				d.Projects, d.ProjectsErr = nil, err.Error()
				return d, nil
			}
			row.Streams[s] = n
		}
		d.Projects = append(d.Projects, row)
	}
	return d, nil
}

// DetailStreams are the stream kinds RedisDetail measures, in display order.
func DetailStreams() []string { return slices.Clone(detailStreams) }
