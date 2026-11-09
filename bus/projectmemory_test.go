package bus

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// projects-admin §3.3 (the person, Q2): the memory a project's keys take,
// read with MEMORY USAGE, never a sibling's; bounded by a key cap and the
// caller's deadline, and partial when cut.
func TestProjectMemory(t *testing.T) {
	testredis.Configure(t)
	c, err := Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	p := "mem" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Cleanup(func() {
		keys, _ := ProjectKeys(ctx, c, p)
		c.Del(ctx, append(keys, p+"-2:big")...)
	})
	for i := range 5 {
		c.Set(ctx, p+":k"+strconv.Itoa(i), string(make([]byte, 1000)), 0)
	}
	c.Set(ctx, p+"-2:big", string(make([]byte, 100000)), 0) // a sibling: never counted
	u, err := ProjectMemory(ctx, c, p, 0)
	if err != nil || u.Keys != 5 || u.Read != 5 || u.Bytes < 5000 || u.Bytes > 50000 || u.Refused {
		t.Fatalf("memory %+v %v", u, err)
	}
	u, err = ProjectMemory(ctx, c, p, 2)
	if err != nil || u.Keys != 5 || u.Read != 2 {
		t.Fatalf("capped at 2: %+v %v", u, err)
	}
	cut, cancel := context.WithCancel(ctx)
	cancel()
	if u, err = ProjectMemory(cut, c, p, 0); err == nil && u.Read == u.Keys && u.Keys > 0 {
		t.Fatalf("a cancelled read said complete: %+v", u)
	}
	if _, err := ProjectMemory(ctx, c, "Bad*", 0); err == nil {
		t.Fatal("an invalid name was read")
	}
	// a broker whose ACL refuses MEMORY: Refused, not an error, nothing summed
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
