package bus

import (
	"context"
	"net"
	"testing"
	"time"
)

// up-broker-ready: a caller with a budget (up waiting for a broker that is starting) bounds the
// whole connect, its PING included, by its context. A listener that accepts and never answers
// stands for a broker that hangs; no Redis is involved.
func TestConnectContextBoundsThePing(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer c.Close() // held open, never answered
		}
	}()
	t.Setenv("REDIS_URL", "redis://"+l.Addr().String()+"/0")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	c, err := ConnectContext(ctx, "")
	if err == nil {
		c.Close()
		t.Fatal("a broker that never answers was connected")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("ConnectContext took %v past its 200 ms context (%v)", elapsed, err)
	}
}
