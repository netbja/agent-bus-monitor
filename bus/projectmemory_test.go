package bus

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// spy records every command a client sends, and runs a hook before a SCAN or
// a pipeline (to cut the context, or delete a key, at a chosen point).
type spy struct {
	cmds         []string
	afterScan    func(n int)
	beforePipe   func(n int)
	scans, pipes int
}

func (s *spy) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, n, a string) (net.Conn, error) { return next(ctx, n, a) }
}
func (s *spy) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, c redis.Cmder) error {
		s.cmds = append(s.cmds, strings.ToLower(c.String()))
		err := next(ctx, c)
		if c.Name() == "scan" && s.afterScan != nil {
			s.scans++
			s.afterScan(s.scans)
		}
		return err
	}
}
func (s *spy) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cs []redis.Cmder) error {
		s.pipes++
		if s.beforePipe != nil {
			s.beforePipe(s.pipes)
		}
		for _, c := range cs {
			s.cmds = append(s.cmds, strings.ToLower(c.String()))
		}
		return next(ctx, cs)
	}
}

func memoryProject(t *testing.T, c *redis.Client, n int) string {
	t.Helper()
	ctx := context.Background()
	p := "mem" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Cleanup(func() {
		keys, _ := ProjectKeys(ctx, c, p)
		c.Del(ctx, append(keys, p+"-2:big")...)
	})
	for i := range n {
		c.Set(ctx, p+":k"+strconv.Itoa(i), strings.Repeat("x", 100+i), 0)
	}
	return p
}

// projects-admin §3.3 (the person, Q2; reviewer-codex): the memory a
// project's keys take, sampled per key (never SAMPLES 0, which walks every
// element on the broker), never a sibling's; the sum is the per-key totals.
func TestProjectMemory(t *testing.T) {
	testredis.Configure(t)
	c, err := Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	p := memoryProject(t, c, 5)
	c.Set(ctx, p+"-2:big", strings.Repeat("y", 100000), 0) // a sibling: never counted
	var want int64
	for i := range 5 {
		n, _ := c.MemoryUsage(ctx, p+":k"+strconv.Itoa(i)).Result()
		want += n
	}
	s := &spy{}
	c.AddHook(s)
	u, err := ProjectMemory(ctx, c, p, 0)
	if err != nil || u.Keys != 5 || u.Read != 5 || u.Bytes != want || u.Refused || u.ScanCut != "" {
		t.Fatalf("memory %+v %v, want %d bytes", u, err, want)
	}
	for _, cmd := range s.cmds {
		if strings.Contains(cmd, "samples") {
			t.Fatalf("a sampled-all command: %q", cmd)
		}
	}
	if _, err := ProjectMemory(ctx, c, "Bad*", 0); err == nil {
		t.Fatal("an invalid name was read")
	}
}

// The bounds: the cap stops the scan itself; a deadline during the scan or
// the reads gives what was read, with no error; a key gone since the scan
// counts as read.
func TestProjectMemoryBounds(t *testing.T) {
	testredis.Configure(t)
	c, err := Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	p := memoryProject(t, c, 250)

	u, err := ProjectMemory(ctx, c, p, 120)
	if err != nil || u.ScanCut != "cap" || u.Keys != 120 || u.Read != 120 {
		t.Fatalf("capped at 120: %+v %v", u, err)
	}

	s := &spy{}
	cut, cancel := context.WithCancel(ctx)
	s.beforePipe = func(n int) {
		if n == 2 {
			cancel() // after the first batch of 100
		}
	}
	c.AddHook(s)
	u, err = ProjectMemory(cut, c, p, 0)
	if err != nil || u.ScanCut != "" || u.Keys != 250 || u.Read != memoryBatch {
		t.Fatalf("cut while reading: %+v %v", u, err)
	}

	// 1200 keys: more than one SCAN COUNT 500 page can cover, so the scan
	// has a second page; the deadline falls right after the first
	big := memoryProject(t, c, 1200)
	c2, _ := Connect("")
	defer c2.Close()
	cut2, cancel2 := context.WithCancel(ctx)
	c2.AddHook(&spy{afterScan: func(n int) {
		if n == 1 {
			cancel2() // during the scan: the total is unknown
		}
	}})
	if u, err = ProjectMemory(cut2, c2, big, 0); err != nil || u.ScanCut != "deadline" || u.Keys >= 1200 || u.Read != 0 {
		t.Fatalf("cut while scanning: %+v %v", u, err)
	}

	c3, _ := Connect("")
	defer c3.Close()
	s3 := &spy{beforePipe: func(n int) {
		if n == 1 {
			c.Del(ctx, p+":k0", p+":k1") // gone since the scan
		}
	}}
	c3.AddHook(s3)
	if u, err = ProjectMemory(ctx, c3, p, 0); err != nil || u.Read != u.Keys || u.Keys != 250 {
		t.Fatalf("keys gone since the scan: %+v %v", u, err)
	}
}

// A broker whose ACL refuses MEMORY: Refused, not an error, nothing summed.
func TestProjectMemoryRefused(t *testing.T) {
	testredis.Configure(t)
	c, err := Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	p := memoryProject(t, c, 5)
	user := "memtest" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := c.Do(ctx, "ACL", "SETUSER", user, "on", ">pw", "~*", "+@all", "-memory").Err(); err != nil {
		t.Skipf("the test broker allows no ACL user: %v", err)
	}
	t.Cleanup(func() { c.Do(ctx, "ACL", "DELUSER", user) })
	o := *c.Options()
	o.Username, o.Password = user, "pw"
	limited := redis.NewClient(&o)
	defer limited.Close()
	if u, err := ProjectMemory(ctx, limited, p, 0); err != nil || !u.Refused || u.Bytes != 0 || u.Keys != 5 {
		t.Fatalf("refused MEMORY: %+v %v", u, err)
	}
}
