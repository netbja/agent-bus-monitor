package bus

import (
	"context"
	"testing"
)

// The broker's facts are read from INFO and DBSIZE.
func TestRedisInfo(t *testing.T) {
	b := dialTest(t)
	ri, err := b.RedisInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ri.Version == "" || ri.UptimeSecs < 0 || ri.UsedMemory == "" || ri.PID == 0 || ri.Keys < 0 {
		t.Errorf("info %+v", ri)
	}
}

// busmon-redis-detail: the Redis card's page reads INFO's memory limits,
// persistence and clients, and each project's keys and stream lengths; it
// writes nothing.
func TestRedisDetail(t *testing.T) {
	b := dialTest(t)
	ctx := context.Background()
	if _, err := b.Notify(ctx, "tester", "one"); err != nil {
		t.Fatal(err)
	}
	d, err := b.RedisDetail(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d.Info.Version == "" || d.PeakMemory == "" || d.MaxMemoryPolicy == "" || d.Clients < 1 || d.RDBStatus == "" {
		t.Errorf("detail %+v", d)
	}
	var mine *ProjectRedis
	for i := range d.Projects {
		if d.Projects[i].Project == b.Project() {
			mine = &d.Projects[i]
		}
	}
	if mine == nil || mine.Keys < 1 || mine.Streams["notify"] != 1 || mine.Streams["cmd"] != 0 {
		t.Fatalf("project row %+v in %+v", mine, d.Projects)
	}
}

// broker-hardening step 3: the server's product, from INFO server: valkey
// when it names itself so (server_name, valkey_version), else redis; the
// version stays redis_version (Valkey keeps a compatibility value there).
func TestParseServerInfoProduct(t *testing.T) {
	redis8 := "# Server\r\nredis_version:8.2.1\r\nprocess_id:7\r\nuptime_in_seconds:30\r\n"
	valkey := "# Server\r\nredis_version:7.2.4\r\nserver_name:valkey\r\nvalkey_version:9.0.7\r\nprocess_id:8\r\n"
	if ri := parseServerInfo(redis8); ri.Product != "redis" || ri.Version != "8.2.1" || ri.PID != 7 || ri.UptimeSecs != 30 {
		t.Fatalf("redis: %+v", ri)
	}
	if ri := parseServerInfo(valkey); ri.Product != "valkey" || ri.Version != "7.2.4" || ri.PID != 8 {
		t.Fatalf("valkey: %+v", ri)
	}
	if ri := parseServerInfo("valkey_version:8.0.1\r\nredis_version:7.2.4\r\n"); ri.Product != "valkey" {
		t.Fatalf("valkey without server_name: %+v", ri)
	}
}
