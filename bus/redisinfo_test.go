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
