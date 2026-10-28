package bus

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
)

// busmon-redis-detail: the dial hook records the address the socket really
// reached, which a host name alone does not say (localhost may be ::1 or
// 127.0.0.1).
func TestRecordingDialerKeepsThePeer(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		if c, err := l.Accept(); err == nil {
			c.Close()
		}
	}()
	var rec atomic.Value
	c, err := recordingDialer(&rec)(context.Background(), "tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if got, _ := rec.Load().(string); got != l.Addr().String() {
		t.Fatalf("peer %q, want %q", got, l.Addr().String())
	}
}

// RedisDetail carries the peer of the client it reads through.
func TestRedisDetailCarriesThePeer(t *testing.T) {
	b := dialTest(t)
	var rec atomic.Value
	rec.Store("127.0.0.1:6380")
	peers.Store(b.r, &rec)
	t.Cleanup(func() { peers.Delete(b.r) })
	d, err := b.RedisDetail(context.Background())
	if err != nil || d.Peer != "127.0.0.1:6380" {
		t.Fatalf("peer %q, %v", d.Peer, err)
	}
}
