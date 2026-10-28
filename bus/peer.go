package bus

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// The connected peer (busmon-redis-detail): the address a client's socket
// really reached. A host name does not say it (localhost may connect to ::1
// or to 127.0.0.1), and busmon's Redis page states a container's facts only
// for the broker it reads.

// peers maps a client Connect made to its last dialed remote address.
var peers sync.Map // *redis.Client -> *atomic.Value (string)

// recordingDialer dials like go-redis's own plain dialer (5 s timeout, 5 min
// keep-alive) and keeps the remote address of each connection it opens.
func recordingDialer(rec *atomic.Value) func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 5 * time.Minute}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := d.DialContext(ctx, network, addr)
		if err == nil {
			rec.Store(c.RemoteAddr().String())
		}
		return c, err
	}
}

// PeerAddr is the remote address the client's last connection reached, ""
// when unknown (a client Connect did not make, a TLS connection, none yet).
func PeerAddr(c *redis.Client) string {
	v, ok := peers.Load(c)
	if !ok {
		return ""
	}
	s, _ := v.(*atomic.Value).Load().(string)
	return s
}
