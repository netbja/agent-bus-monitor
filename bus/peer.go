package bus

import (
	"context"
	"net"
	"sync"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
)

// The connected peer (busmon-redis-detail): the address a client's socket
// really reached. A host name does not say it (localhost may connect to ::1
// or to 127.0.0.1), and busmon's Redis page states a container's facts only
// for the broker it reads.

// peers maps a client Connect made to its last dialed remote address.
var peers sync.Map // *redis.Client -> *atomic.Value (string)

// recordingDialer dials with base (Connect passes go-redis's own
// redis.NewDialer, so dial_timeout, TLS and keep-alive are unchanged) and
// keeps the remote address of each connection it opens.
func recordingDialer(rec *atomic.Value, base func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := base(ctx, network, addr)
		if err == nil {
			rec.Store(c.RemoteAddr().String())
		}
		return c, err
	}
}

// PeerAddr is the remote address the client's last connection reached, ""
// when unknown (a client Connect did not make, none yet).
func PeerAddr(c *redis.Client) string {
	v, ok := peers.Load(c)
	if !ok {
		return ""
	}
	s, _ := v.(*atomic.Value).Load().(string)
	return s
}
