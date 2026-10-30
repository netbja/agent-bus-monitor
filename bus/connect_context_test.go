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

// reviewer-codex: go-redis's TLS dial ignores the context (tls.DialWithDialer, bound only by
// dial_timeout, 5 s). The caller is bounded anyway: go-redis v9's pool dials in its own goroutine
// and the caller returns at ctx.Done(), the abandoned dial ending in the background on the client
// ConnectContext closes. This pins that a peer that accepts and stalls the handshake cannot hold
// a caller past its context, should a go-redis update change how the pool waits.
func TestConnectContextBoundsATLSHandshake(t *testing.T) {
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
			defer c.Close() // accepts, never says a word of TLS
		}
	}()
	t.Setenv("REDIS_URL", "rediss://"+l.Addr().String()+"/0")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	c, err := ConnectContext(ctx, "")
	if err == nil {
		c.Close()
		t.Fatal("a stalled TLS handshake was connected")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("ConnectContext took %v past its 200 ms context over TLS (%v)", elapsed, err)
	}
}
