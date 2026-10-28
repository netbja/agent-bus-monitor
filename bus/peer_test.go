package bus

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
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
	var d net.Dialer
	c, err := recordingDialer(&rec, d.DialContext)(context.Background(), "tcp", l.Addr().String())
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

// Connect installs the hook: a client it returns knows the address its
// connection reached (here the disposable broker's socket or host:port).
func TestConnectRecordsThePeer(t *testing.T) {
	b := dialTest(t)
	peer := PeerAddr(b.r)
	if peer == "" {
		t.Fatal("Connect's client has no peer: the dial hook is not installed")
	}
	raw := os.Getenv("REDIS_URL")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := u.Host
	if u.Scheme == "unix" {
		want = u.Path
	}
	if peer != want {
		t.Fatalf("peer %q, want %q (REDIS_URL %s)", peer, want, u.Redacted())
	}
}

// A dial_timeout in REDIS_URL still reaches the dial: the hook wraps
// go-redis's own dialer instead of fixing a timeout of its own.
func TestRecordingDialerKeepsTheBaseDialer(t *testing.T) {
	var rec atomic.Value
	called := false
	base := func(context.Context, string, string) (net.Conn, error) { called = true; return nil, errors.New("base") }
	if _, err := recordingDialer(&rec, base)(context.Background(), "tcp", "x:1"); err == nil || !called {
		t.Fatalf("base not used: %v", err)
	}
	if rec.Load() != nil {
		t.Fatal("a failed dial recorded a peer")
	}
}
