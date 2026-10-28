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
