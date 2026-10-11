package mission

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// dialBus opens a fresh project on the isolated test broker and deletes it
// after the test.
func dialBus(t *testing.T) (*bus.Bus, *redis.Client) {
	t.Helper()
	testredis.Configure(t)
	r, err := bus.Connect("")
	if err != nil {
		t.Fatalf("explicit test Redis unavailable: %v", err)
	}
	b, err := bus.Open(r, "m"+strconv.FormatInt(time.Now().UnixNano(), 36))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = bus.DeleteProject(context.Background(), r, b.Project()) })
	return b, r
}

func leasedStore(t *testing.T) (Store, *redis.Client) {
	b, r := dialBus(t)
	if ok, err := b.AcquireConductor(context.Background(), "tok", time.Minute); err != nil || !ok {
		t.Fatalf("lease: %v %v", ok, err)
	}
	return Store{B: b, Token: "tok"}, r
}

func TestStoreRoundTrip(t *testing.T) {
	s, _ := leasedStore(t)
	ctx := context.Background()
	ch := ChangeRecord{Change: "chg", Path: "docs/changes/chg/missions.json", Commit: "abc", FileHash: "h", Base: "main", Branch: "change/chg", Rev: 1, State: "active", LoadedAt: 42}
	if err := s.PutChange(ctx, ch); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Change(ctx, "chg")
	if err != nil || !ok || !reflect.DeepEqual(got, ch) {
		t.Fatalf("change: %+v %v %v", got, ok, err)
	}
	m := MissionRecord{ID: "t1", Hash: "mh", State: InReview, Attempt: 2, Rejections: 1,
		Current:  Attempt{N: 2, Task: "chg-t1-2", Agent: "kimi", Base: "b", Commit: "c", Branch: "mission/t1"},
		History:  []Attempt{{N: 1, Task: "chg-t1-1", Agent: "kimi", Outcome: "check_failed", Reason: "red"}},
		Reviewer: "codex", Subject: "mission:chg/t1@c", ReviewRef: "chg-t1-2", LogPath: "/l", CheckTail: "ok",
		PreHead: "p", TestedHead: "th", TestedOK: true, Reason: "r"}
	if err := s.PutMission(ctx, "chg", m); err != nil {
		t.Fatal(err)
	}
	all, err := s.Missions(ctx, "chg")
	if err != nil {
		t.Fatal(err)
	}
	g := all["t1"]
	if g.Updated == 0 {
		t.Fatal("Updated not stamped from Redis TIME")
	}
	g.Updated = 0
	if !reflect.DeepEqual(g, m) {
		t.Fatalf("mission:\n got %+v\nwant %+v", g, m)
	}
}

func TestStoreStaleToken(t *testing.T) {
	s, _ := leasedStore(t)
	s.Token = "lost"
	if err := s.PutMission(context.Background(), "chg", MissionRecord{ID: "t1"}); !errors.Is(err, bus.ErrNotHolder) {
		t.Fatalf("err = %v, want ErrNotHolder", err)
	}
}

func TestStoreChangesScan(t *testing.T) {
	s, r := leasedStore(t)
	ctx := context.Background()
	s.PutChange(ctx, ChangeRecord{Change: "aaa", State: "active"})
	s.PutChange(ctx, ChangeRecord{Change: "bbb", State: "active"})
	r.HSet(ctx, s.B.Project()+":change:Bad Name", "record", "{}")
	got, err := s.Changes(ctx)
	if err != nil || !reflect.DeepEqual(got, []string{"aaa", "bbb"}) {
		t.Fatalf("Changes = %v %v", got, err)
	}
}
