package bus

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// A connected tunnel can blackhole replies without refusing new connections.
// Proxy only this test's isolated broker and drop replies AFTER Connect's ping.
func TestConnectHonorsDeadlineOnEstablishedConnection(t *testing.T) {
	upstream := dialTest(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var drop atomic.Bool
	peers := make(chan [2]net.Conn, 1)
	ready := make(chan error, 1)
	go func() {
		downstream, err := listener.Accept()
		if err != nil {
			ready <- err
			return
		}
		opts := upstream.r.Options()
		remote, err := net.Dial(opts.Network, opts.Addr)
		if err != nil {
			downstream.Close()
			ready <- err
			return
		}
		peers <- [2]net.Conn{downstream, remote}
		ready <- nil
		go func() { _, _ = io.Copy(remote, downstream) }()
		buffer := make([]byte, 32768)
		for {
			n, err := remote.Read(buffer)
			if n > 0 && !drop.Load() {
				if _, werr := downstream.Write(buffer[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		select {
		case pair := <-peers:
			pair[0].Close()
			pair[1].Close()
		default:
		}
	})
	t.Setenv("REDIS_URL", "redis://"+listener.Addr().String()+"/0")
	client, err := Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	drop.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = client.Get(ctx, "test-only-deadline-key").Err()
	if err == nil {
		t.Fatal("blackholed request unexpectedly succeeded")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("context deadline ignored on established tunnel: %s (%v)", elapsed, err)
	}
}
